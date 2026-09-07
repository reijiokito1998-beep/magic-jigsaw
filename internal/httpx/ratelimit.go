package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/logx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
)

// visitor is a single client's token-bucket state.
type visitor struct {
	tokens   float64
	lastSeen time.Time
}

// RateLimiter is an in-memory, per-client token-bucket rate limiter. It is safe
// for concurrent use and evicts idle clients periodically so memory stays
// bounded under churn. It is intentionally dependency-free.
//
// For multi-instance deployments this limits each instance independently; put a
// shared limiter (e.g. at the load balancer / API gateway) in front for a
// global cap.
type RateLimiter struct {
	mu         sync.Mutex
	visitors   map[string]*visitor
	scope      string        // metric label identifying which limiter this is
	ratePerSec float64       // tokens refilled per second
	burst      float64       // bucket capacity (max burst)
	ttl        time.Duration // idle eviction window
	trustProxy bool          // honor X-Forwarded-For / X-Real-IP
}

// NewRateLimiter builds a limiter allowing ratePerSec sustained requests with a
// short burst of burst. scope names the limiter in metrics ("global", "auth")
// so rejections can be attributed. When trustProxy is true the client IP is
// taken from X-Forwarded-For / X-Real-IP (only enable behind a trusted reverse
// proxy).
func NewRateLimiter(scope string, ratePerSec, burst float64, trustProxy bool) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	rl := &RateLimiter{
		visitors:   make(map[string]*visitor),
		scope:      scope,
		ratePerSec: ratePerSec,
		burst:      burst,
		ttl:        10 * time.Minute,
		trustProxy: trustProxy,
	}
	// Never evict a client before its bucket would have refilled completely,
	// otherwise a slow limiter (e.g. 5/hour) could be reset simply by idling
	// past the eviction window.
	if ratePerSec > 0 {
		if refill := time.Duration(burst / ratePerSec * float64(time.Second)); refill > rl.ttl {
			rl.ttl = refill
		}
	}
	// Pre-create the series so a limiter that has never rejected anything still
	// reports 0 instead of being absent from the scrape.
	metrics.RateLimitedTotal.WithLabelValues(scope).Add(0)
	slog.Info("rate limiter initialized",
		slog.String("component", "ratelimit"),
		slog.String("scope", scope),
		slog.Float64("rate_per_sec", ratePerSec),
		slog.Float64("burst", burst),
		slog.Bool("trust_proxy", trustProxy),
	)
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	t := time.NewTicker(time.Minute)
	for range t.C {
		rl.mu.Lock()
		for k, v := range rl.visitors {
			if time.Since(v.lastSeen) > rl.ttl {
				delete(rl.visitors, k)
			}
		}
		rl.mu.Unlock()
	}
}

// allow consumes one token for key. When the bucket is empty it returns false
// plus how long the caller must wait before a token is available again.
func (rl *RateLimiter) allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, ok := rl.visitors[key]
	if !ok {
		rl.visitors[key] = &visitor{tokens: rl.burst - 1, lastSeen: now}
		return true, 0
	}

	// Refill proportionally to elapsed time, capped at burst.
	elapsed := now.Sub(v.lastSeen).Seconds()
	v.tokens += elapsed * rl.ratePerSec
	if v.tokens > rl.burst {
		v.tokens = rl.burst
	}
	v.lastSeen = now

	if v.tokens < 1 {
		return false, rl.retryAfter(v.tokens)
	}
	v.tokens--
	return true, 0
}

// retryAfter is how long it takes to accumulate one whole token from the
// current balance, rounded up to a whole second (the Retry-After unit).
func (rl *RateLimiter) retryAfter(tokens float64) time.Duration {
	if rl.ratePerSec <= 0 {
		// The bucket never refills; advise a generic backoff.
		return time.Minute
	}
	d := time.Duration((1 - tokens) / rl.ratePerSec * float64(time.Second))
	if d < time.Second {
		return time.Second
	}
	return d.Round(time.Second)
}

// Middleware enforces the limiter, keyed by client IP, on the wrapped handler.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.clientIP(r)
		if ok, retryAfter := rl.allow(key); !ok {
			metrics.RateLimitedTotal.WithLabelValues(rl.scope).Inc()
			logx.FromContext(r.Context()).Warn("request rate-limited",
				slog.String("component", "ratelimit"),
				slog.String("scope", rl.scope),
				slog.String("client", key),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Float64("rate_per_sec", rl.ratePerSec),
				slog.Float64("burst", rl.burst),
				slog.Duration("retry_after", retryAfter),
			)
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			ErrorCtx(r.Context(), w, http.StatusTooManyRequests, "rate_limited", "too many requests, please slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP resolves the client address. It only trusts forwarding headers when
// trustProxy is set, otherwise header spoofing would defeat IP rate limiting.
func (rl *RateLimiter) clientIP(r *http.Request) string {
	if rl.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The left-most entry is the original client.
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

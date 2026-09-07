package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// hourlyLimiter mirrors how the auth routes are wired: N per rolling hour,
// with all N spendable back-to-back.
func hourlyLimiter(scope string, perHour float64) *RateLimiter {
	return NewRateLimiter(scope, perHour/3600, perHour, false)
}

// TestHourlyLimitAllowsExactlyNPerIP pins the auth policy: the first N requests
// from an IP succeed, the next one is rejected with a truthful Retry-After, and
// other IPs are unaffected.
func TestHourlyLimitAllowsExactlyNPerIP(t *testing.T) {
	cases := []struct {
		scope   string
		perHour int
	}{
		{"test-register", 5},
		{"test-login", 20},
	}

	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			rl := hourlyLimiter(tc.scope, float64(tc.perHour))
			h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			call := func(ip string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/x", nil)
				req.RemoteAddr = ip + ":1234"
				h.ServeHTTP(rec, req)
				return rec
			}
			// Distinct client per subtest, so buckets never bleed across cases.
			client := fmt.Sprintf("10.1.1.%d", tc.perHour)

			for i := 1; i <= tc.perHour; i++ {
				if rec := call(client); rec.Code != http.StatusOK {
					t.Fatalf("request %d status = %d, want 200", i, rec.Code)
				}
			}

			rec := call(client)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d status = %d, want 429", tc.perHour+1, rec.Code)
			}

			// Retry-After must reflect the real wait for one refilled token,
			// not a token 1 second.
			secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
			if err != nil {
				t.Fatalf("Retry-After = %q, want an integer", rec.Header().Get("Retry-After"))
			}
			want := 3600 / tc.perHour
			if secs < want-2 || secs > want {
				t.Errorf("Retry-After = %ds, want ~%ds", secs, want)
			}

			// A different IP has its own bucket.
			if rec := call("10.2.2.2"); rec.Code != http.StatusOK {
				t.Errorf("other IP status = %d, want 200", rec.Code)
			}
		})
	}
}

// TestIdleEvictionCannotResetASlowBucket guards the limit against being reset
// by simply idling: eviction must never happen before a full refill.
func TestIdleEvictionCannotResetASlowBucket(t *testing.T) {
	rl := hourlyLimiter("test-ttl", 5)
	if rl.ttl < time.Hour {
		t.Errorf("ttl = %s, want >= 1h so an idle client keeps its spent tokens", rl.ttl)
	}
}

// TestZeroRateLimiterKeepsDefaultTTL covers the never-refilling limiter used in
// tests: the refill time is infinite, so the default idle window must stand.
func TestZeroRateLimiterKeepsDefaultTTL(t *testing.T) {
	rl := NewRateLimiter("test-zero", 0, 1, false)
	if rl.ttl != 10*time.Minute {
		t.Errorf("ttl = %s, want 10m", rl.ttl)
	}
}

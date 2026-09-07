package api

import (
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
)

// maxBodyBytes is a generous global request-body ceiling (above the multipart
// upload limit). It is a coarse safety net; JSON/upload handlers cap tighter.
// Raised to accommodate the batch library upload endpoint (100MB total).
const maxBodyBytes = 120 << 20 // 120 MB

// Handler builds the fully-wired HTTP handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Operational endpoints (public): /healthz, /readyz, /version, /metrics.
	s.registerOpsRoutes(mux)

	// Swagger UI (public) at /swagger/index.html; spec at /swagger/doc.json.
	// Disabled in production to avoid publishing the API surface.
	if s.cfg.EnableSwagger {
		mux.Handle("GET /swagger/", httpSwagger.WrapHandler)
	}

	// Auth (public) — each endpoint gets its own per-IP hourly budget, well
	// below the global limiter. Account creation is a write that can be
	// scripted into spam; login is the credential-stuffing surface. Buckets
	// refill continuously, so the cap is a sliding hour rather than a reset on
	// the clock hour. Login is the looser of the two because legitimate users
	// mistype passwords.
	registerLimiter := httpx.NewRateLimiter("register", s.cfg.RegisterRatePerHour/3600, s.cfg.RegisterRateBurst, s.cfg.TrustProxy)
	loginLimiter := httpx.NewRateLimiter("login", s.cfg.LoginRatePerHour/3600, s.cfg.LoginRateBurst, s.cfg.TrustProxy)
	mux.Handle("POST /api/v1/auth/register", registerLimiter.Middleware(http.HandlerFunc(s.handleRegister)))
	mux.Handle("POST /api/v1/auth/login", loginLimiter.Middleware(http.HandlerFunc(s.handleLogin)))

	// Authenticated routes.
	authed := httpx.RequireAuth(s.jwt)
	protect := func(h http.HandlerFunc) http.Handler { return authed(h) }

	// Me / notifications / reputation / stats.
	mux.Handle("GET /api/v1/me", protect(s.handleMe))
	mux.Handle("GET /api/v1/me/notifications", protect(s.handleNotifications))
	mux.Handle("GET /api/v1/me/points", protect(s.handleGetPoints))
	mux.Handle("POST /api/v1/me/points/spend", protect(s.handleSpendPoints))
	mux.Handle("POST /api/v1/me/points/purchase", protect(s.handleConfirmPurchase))
	mux.Handle("GET /api/v1/me/results", protect(s.handleMyResults))
	mux.Handle("GET /api/v1/me/stats", protect(s.handleMyStats))
	mux.Handle("GET /api/v1/me/history", protect(s.handleMyHistory))
	mux.Handle("GET /api/v1/leaderboard", protect(s.handleLeaderboard))

	// Collection.
	mux.Handle("GET /api/v1/collection", protect(s.handleListCollection))
	mux.Handle("POST /api/v1/collection", protect(s.handleAddToCollection))
	mux.Handle("POST /api/v1/collection/upload", protect(s.handleUploadCollection))
	mux.Handle("DELETE /api/v1/collection/{imageId}", protect(s.handleRemoveFromCollection))

	// Daily check-in.
	mux.Handle("GET /api/v1/checkin", protect(s.handleGetCheckin))
	mux.Handle("POST /api/v1/checkin", protect(s.handleCheckin))

	// Expedition (100-level campaign).
	mux.Handle("GET /api/v1/expedition", protect(s.handleExpedition))
	mux.Handle("POST /api/v1/expedition/{level}/start", protect(s.handleStartExpedition))
	mux.Handle("POST /api/v1/expedition/{level}/complete", protect(s.handleCompleteExpedition))
	mux.Handle("POST /api/v1/expedition/{level}/fail", protect(s.handleFailExpedition))

	// Stories (narrative book campaigns).
	mux.Handle("GET /api/v1/stories", protect(s.handleListStories))
	mux.Handle("GET /api/v1/stories/{id}", protect(s.handleGetStory))
	mux.Handle("POST /api/v1/stories/{id}/pages/{pageId}/complete", protect(s.handleCompleteStoryPage))

	// Dungeons (6x6 puzzle-battle map campaigns).
	mux.Handle("GET /api/v1/dungeons", protect(s.handleListDungeons))
	mux.Handle("GET /api/v1/dungeons/{id}", protect(s.handleGetDungeon))
	mux.Handle("POST /api/v1/dungeons/{id}/move", protect(s.handleMoveDungeon))
	mux.Handle("POST /api/v1/dungeons/{id}/retreat", protect(s.handleRetreatDungeon))
	mux.Handle("POST /api/v1/dungeons/{id}/monsters/{monsterId}/complete", protect(s.handleCompleteDungeonMonster))
	mux.Handle("POST /api/v1/dungeons/{id}/complete", protect(s.handleCompleteDungeon))
	mux.Handle("GET /api/v1/dungeon-treasures", protect(s.handleListDungeonTreasures))

	// Hidden Secrets (a picture behind a 3x3 wall of puzzle-locked shutters).
	mux.Handle("GET /api/v1/hidden-secrets", protect(s.handleListHiddenSecrets))
	mux.Handle("GET /api/v1/hidden-secrets/{id}", protect(s.handleGetHiddenSecret))
	mux.Handle("POST /api/v1/hidden-secrets/{id}/tiles/{tileIndex}/reveal", protect(s.handleRevealHiddenSecretTile))
	mux.Handle("GET /api/v1/hidden-secret-unlocks", protect(s.handleListHiddenSecretUnlocks))

	// Challenges.
	mux.Handle("GET /api/v1/challenges/today", protect(s.handleTodayChallenge))
	mux.Handle("GET /api/v1/challenges", protect(s.handleListChallenges))
	mux.Handle("POST /api/v1/challenges/{id}/start", protect(s.handleStartChallenge))
	mux.Handle("POST /api/v1/challenges/{id}/complete", protect(s.handleCompleteChallenge))
	mux.Handle("POST /api/v1/challenges/{id}/fail", protect(s.handleFailChallenge))
	mux.Handle("GET /api/v1/challenges/{id}/quiz", protect(s.handleChallengeQuiz))
	mux.Handle("POST /api/v1/challenges/{id}/quiz", protect(s.handleAnswerChallengeQuiz))

	// Admin (authenticated + admin role).
	admin := func(h http.HandlerFunc) http.Handler {
		return authed(httpx.RequireAdmin(h))
	}
	mux.Handle("POST /api/v1/admin/challenges", admin(s.handleAdminCreateChallenge))
	mux.Handle("PUT /api/v1/admin/challenges/{id}", admin(s.handleAdminUpdateChallenge))
	mux.Handle("GET /api/v1/admin/challenges", admin(s.handleAdminListChallenges))
	mux.Handle("GET /api/v1/admin/expedition", admin(s.handleAdminListExpedition))
	mux.Handle("POST /api/v1/admin/expedition", admin(s.handleAdminCreateExpeditionLevel))
	mux.Handle("POST /api/v1/admin/expedition/{level}", admin(s.handleAdminUploadExpedition))
	mux.Handle("DELETE /api/v1/admin/expedition/{level}", admin(s.handleAdminDeleteExpeditionLevel))
	mux.Handle("PUT /api/v1/admin/expedition/{level}/config", admin(s.handleAdminUpdateExpeditionConfig))
	mux.Handle("GET /api/v1/admin/stories", admin(s.handleAdminListStories))
	mux.Handle("POST /api/v1/admin/stories", admin(s.handleAdminCreateStory))
	mux.Handle("PUT /api/v1/admin/stories/{id}/config", admin(s.handleAdminUpdateStoryConfig))
	mux.Handle("POST /api/v1/admin/stories/{id}/pages", admin(s.handleAdminAddStoryPage))
	mux.Handle("PUT /api/v1/admin/stories/{id}/pages/{pageId}", admin(s.handleAdminUpdateStoryPage))
	mux.Handle("GET /api/v1/admin/dungeons", admin(s.handleAdminListDungeons))
	mux.Handle("POST /api/v1/admin/dungeons", admin(s.handleAdminCreateDungeon))
	mux.Handle("PUT /api/v1/admin/dungeons/{id}", admin(s.handleAdminUpdateDungeon))
	mux.Handle("DELETE /api/v1/admin/dungeons/{id}", admin(s.handleAdminDeleteDungeon))
	mux.Handle("GET /api/v1/admin/hidden-secrets", admin(s.handleAdminListHiddenSecrets))
	mux.Handle("POST /api/v1/admin/hidden-secrets", admin(s.handleAdminCreateHiddenSecret))
	mux.Handle("PUT /api/v1/admin/hidden-secrets/{id}", admin(s.handleAdminUpdateHiddenSecret))
	mux.Handle("DELETE /api/v1/admin/hidden-secrets/{id}", admin(s.handleAdminDeleteHiddenSecret))
	mux.Handle("GET /api/v1/admin/library", admin(s.handleAdminListLibrary))
	mux.Handle("POST /api/v1/admin/library", admin(s.handleAdminUploadLibrary))
	mux.Handle("POST /api/v1/admin/library/batch", admin(s.handleAdminUploadLibraryBatch))
	mux.Handle("PATCH /api/v1/admin/library/{id}", admin(s.handleAdminRenameLibrary))
	mux.Handle("DELETE /api/v1/admin/library/{id}", admin(s.handleAdminDeleteLibrary))

	// Global middleware (outermost first).
	//
	// Ordering rationale:
	//   RequestID   — first, so every later line can be correlated.
	//   Recover     — catches panics from everything below it.
	//   Telemetry   — above the rate limiter, so rejected (429) requests are
	//                 still counted and timed; health/metrics paths are excluded
	//                 from the access log but not from metrics.
	//   CORS        — innermost of the globals: preflight OPTIONS still gets
	//                 security headers and is still measured.
	globalLimiter := httpx.NewRateLimiter("global", s.cfg.RateLimitPerSec, s.cfg.RateLimitBurst, s.cfg.TrustProxy)
	return httpx.Chain(mux,
		httpx.RequestID,
		httpx.Recover,
		httpx.Telemetry(mux, "/healthz", "/readyz", s.cfg.MetricsPath),
		httpx.SecurityHeaders,
		httpx.MaxBytes(maxBodyBytes),
		globalLimiter.Middleware,
		httpx.CORS(s.cfg.CORSOrigins),
	)
}

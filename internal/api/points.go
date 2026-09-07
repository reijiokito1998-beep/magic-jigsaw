package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/purchases"
)

type pointsResponse struct {
	Points int `json:"points"`
}

// purchaseResponse is what a confirmed purchase leaves the user holding. Stars
// are the user's full expedition star total (earned + purchased), so the client
// can show it without a second round-trip.
type purchaseResponse struct {
	Points          int `json:"points"`
	ExpeditionStars int `json:"expeditionStars"`
}

type spendPointsRequest struct {
	Amount int `json:"amount"`
}

// confirmPurchaseRequest is what the Flutter client posts after a store
// purchase completes. Source is the in_app_purchase plugin's
// PurchaseVerificationData.source ("app_store" or "google_play") and
// VerificationData its serverVerificationData (Play purchase token / base64
// App Store receipt). Notably absent: any point amount -- the payout comes
// from the server-side catalogue only.
type confirmPurchaseRequest struct {
	ProductID        string `json:"productId"`
	Source           string `json:"source"`
	VerificationData string `json:"verificationData"`
}

// handleGetPoints returns the caller's unlock-point balance.
//
// @Summary      Unlock points balance
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  pointsResponse
// @Router       /api/v1/me/points [get]
func (s *Server) handleGetPoints(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleGetPoints: start userID=%s", uid)
	n, err := s.store.GetUnlockPoints(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleGetPoints: error GetUnlockPoints userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load points")
		return
	}
	httpx.JSON(w, http.StatusOK, pointsResponse{Points: n})
}

// handleSpendPoints deducts unlock points (default 1). Used to buy +30s in
// Expedition / Story games.
//
// @Summary      Spend unlock points
// @Tags         me
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body  spendPointsRequest  false  "Amount to spend (default 1)"
// @Success      200  {object}  pointsResponse
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/me/points/spend [post]
func (s *Server) handleSpendPoints(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	var req spendPointsRequest
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleSpendPoints: error decoding request body userID=%s", uid)
		return
	}
	if req.Amount <= 0 {
		req.Amount = 1
	}
	log.Printf("[DEBUG] handleSpendPoints: start userID=%s amount=%d", uid, req.Amount)
	balance, ok, err := s.store.SpendUnlockPoints(r.Context(), uid, req.Amount)
	if err != nil {
		log.Printf("[DEBUG] handleSpendPoints: error SpendUnlockPoints userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not spend points")
		return
	}
	if !ok {
		log.Printf("[DEBUG] handleSpendPoints: insufficient points userID=%s amount=%d", uid, req.Amount)
		httpx.Error(w, http.StatusConflict, "insufficient_points", "not enough unlock points")
		return
	}
	metrics.PointsTotal.WithLabelValues("spent", "gameplay").Add(float64(req.Amount))
	log.Printf("[DEBUG] handleSpendPoints: success userID=%s amount=%d balance=%d", uid, req.Amount, balance)
	httpx.JSON(w, http.StatusOK, pointsResponse{Points: balance})
}

// handleConfirmPurchase verifies an in-app purchase with Apple/Google and
// credits the product's reward (unlock points and/or expedition stars).
//
// The client's productId only selects a payout from the server-side catalogue;
// the store decides whether the purchase is real. Crediting is idempotent per
// store transaction, so a retried or restored purchase returns the unchanged
// balances with 200 rather than paying out twice.
//
// @Summary      Confirm an in-app purchase and credit its reward
// @Tags         me
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body  confirmPurchaseRequest  true  "Store product id, source and verification data"
// @Success      200  {object}  purchaseResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      402  {object}  httpx.ErrorBody
// @Failure      503  {object}  httpx.ErrorBody
// @Router       /api/v1/me/points/purchase [post]
func (s *Server) handleConfirmPurchase(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	var req confirmPurchaseRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleConfirmPurchase: error decoding request body userID=%s", uid)
		return
	}
	log.Printf("[DEBUG] handleConfirmPurchase: start userID=%s product=%s source=%s", uid, req.ProductID, req.Source)

	reward, known := purchases.RewardFor(req.ProductID)
	if !known {
		log.Printf("[DEBUG] handleConfirmPurchase: unknown product userID=%s product=%s", uid, req.ProductID)
		httpx.Error(w, http.StatusBadRequest, "unknown_product", "unknown product id")
		return
	}

	if !purchases.IsKnownPlatform(req.Source) {
		log.Printf("[DEBUG] handleConfirmPurchase: unknown source userID=%s source=%s", uid, req.Source)
		httpx.Error(w, http.StatusBadRequest, "unknown_source", "unknown purchase source")
		return
	}
	verifier, ok := s.verifiers.For(req.Source)
	if !ok {
		// Known platform, but this deployment has no store credentials for it.
		// An operator problem, so report it as retryable rather than denying.
		metrics.PurchaseVerificationsTotal.WithLabelValues(req.Source, "unsupported").Inc()
		log.Printf("[DEBUG] handleConfirmPurchase: no verifier configured userID=%s source=%s", uid, req.Source)
		httpx.Error(w, http.StatusServiceUnavailable, "verification_unavailable",
			"purchase verification is not available for this platform")
		return
	}

	verifyStart := time.Now()
	verified, err := verifier.Verify(r.Context(), req.ProductID, req.VerificationData)
	metrics.PurchaseVerifyDuration.WithLabelValues(req.Source).Observe(time.Since(verifyStart).Seconds())
	if err != nil {
		// A rise here means paying players are not being credited, so it is a
		// revenue alert rather than a mere error-rate blip.
		metrics.PurchaseVerificationsTotal.WithLabelValues(req.Source, "failure").Inc()
		// Log the detail, tell the client only whether to retry.
		log.Printf("[DEBUG] handleConfirmPurchase: verify failed userID=%s product=%s source=%s: %v",
			uid, req.ProductID, req.Source, err)
		if errors.Is(err, purchases.ErrNotVerified) {
			httpx.Error(w, http.StatusPaymentRequired, "purchase_not_verified", "purchase could not be verified")
			return
		}
		httpx.Error(w, http.StatusServiceUnavailable, "verification_unavailable",
			"could not reach the store to verify this purchase, please retry")
		return
	}

	balances, credited, err := s.store.CreditPurchase(
		r.Context(), uid, req.ProductID, req.Source, verified.TransactionID,
		reward.Points, reward.ExpeditionStars)
	if err != nil {
		log.Printf("[DEBUG] handleConfirmPurchase: error CreditPurchase userID=%s product=%s: %v",
			uid, req.ProductID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not credit purchase")
		return
	}

	// The user's star total mixes purchased with earned stars; recompute it so
	// the client does not have to guess. A failure here only costs the client
	// an up-to-date star count, so the purchase is still reported as success.
	totalStars, err := s.store.TotalExpeditionStars(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleConfirmPurchase: error TotalExpeditionStars userID=%s: %v", uid, err)
		totalStars = balances.BonusStars
	}

	metrics.PurchaseVerificationsTotal.WithLabelValues(req.Source, "success").Inc()
	// credited is false for a replayed/restored transaction; counting only real
	// credits keeps the points series aligned with actual payouts.
	if credited {
		metrics.PointsTotal.WithLabelValues("granted", "purchase").Add(float64(reward.Points))
	}
	log.Printf("[DEBUG] handleConfirmPurchase: success userID=%s product=%s credited=%t points=%d stars=%d",
		uid, req.ProductID, credited, balances.Points, totalStars)
	httpx.JSON(w, http.StatusOK, purchaseResponse{Points: balances.Points, ExpeditionStars: totalStars})
}

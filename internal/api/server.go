// Package api wires HTTP handlers to the service/repository layer.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/reijiokito/jigsaw-backend/internal/auth"
	"github.com/reijiokito/jigsaw-backend/internal/config"
	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/purchases"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
	"github.com/reijiokito/jigsaw-backend/internal/storage"
)

// Server bundles all dependencies shared by the HTTP handlers.
type Server struct {
	cfg   *config.Config
	store *repository.Store
	cloud *storage.Cloudinary
	jwt   *auth.JWTManager
	// verifiers holds an in-app-purchase verifier per store platform. Platforms
	// without configured credentials are absent, and their purchases are
	// rejected rather than credited unverified.
	verifiers purchases.Verifiers
}

// NewServer constructs the API server.
func NewServer(
	cfg *config.Config,
	store *repository.Store,
	cloud *storage.Cloudinary,
	jwt *auth.JWTManager,
	verifiers purchases.Verifiers,
) *Server {
	return &Server{cfg: cfg, store: store, cloud: cloud, jwt: jwt, verifiers: verifiers}
}

// maxJSONBytes caps the size of a JSON request body (uploads use their own,
// larger multipart limit). Anything larger is almost certainly abuse.
const maxJSONBytes = 1 << 20 // 1 MB

// decodeJSON reads and validates a JSON request body. The body is size-limited
// so a malicious client cannot exhaust memory with a huge payload.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		log.Printf("invalid JSON body: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

// buildChallengeView attaches the authenticated user's result (if any) to a
// challenge.
func (s *Server) buildChallengeView(ctx context.Context, c models.DailyChallenge) (models.ChallengeView, error) {
	view := models.ChallengeView{DailyChallenge: c}
	res, err := s.store.GetResult(ctx, httpx.UserIDFrom(ctx), c.ID)
	if err == nil {
		view.Result = &res
	} else if !errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] buildChallengeView: error GetResult challengeID=%s: %v", c.ID, err)
		return models.ChallengeView{}, err
	}
	return view, nil
}

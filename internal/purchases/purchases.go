// Package purchases verifies in-app-purchase receipts server-to-server with
// Apple and Google.
//
// Nothing here trusts the client: the app tells us which product it thinks it
// bought, but the store is the only authority on whether a purchase actually
// happened and on the transaction identity used to make crediting idempotent
// (see repository.CreditPurchase).
package purchases

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/config"
)

// Platform values, matching the in_app_purchase plugin's
// PurchaseVerificationData.source strings sent by the Flutter client.
const (
	PlatformAppStore   = "app_store"
	PlatformGooglePlay = "google_play"
)

// ErrNotVerified means the store answered, and the answer was "this purchase is
// not valid" (bad receipt, wrong app, cancelled/pending, no such transaction).
// Errors that do not wrap it are infrastructure failures (network, bad
// credentials, store outage) and are worth retrying.
var ErrNotVerified = errors.New("purchase not verified")

// VerifiedPurchase is what a store confirmed about a purchase.
type VerifiedPurchase struct {
	// TransactionID uniquely identifies this one purchase event: Apple's
	// transaction_id, or the Google Play purchase token. It is the idempotency
	// key for crediting points.
	TransactionID string
}

// Verifier checks a single store's purchase proof.
type Verifier interface {
	Verify(ctx context.Context, productID, verificationData string) (VerifiedPurchase, error)
}

// IsKnownPlatform reports whether source is a store this server understands.
// A false result means the client sent something we never issue purchases for.
func IsKnownPlatform(source string) bool {
	return source == PlatformAppStore || source == PlatformGooglePlay
}

// Verifiers holds the configured verifier per platform. A missing key means
// that platform has no credentials configured, so its purchases cannot be
// credited.
type Verifiers map[string]Verifier

// For returns the verifier for a platform, or ok=false when that platform is
// unknown or not configured.
func (v Verifiers) For(platform string) (Verifier, bool) {
	ver, ok := v[platform]
	return ver, ok
}

// maxResponseBytes caps how much of a store's reply we read. Apple receipts can
// be large; anything past this is a malformed or hostile response.
const maxResponseBytes = 4 << 20 // 4 MB

// newHTTPClient returns the client used for store calls. The timeout is a
// backstop: callers also pass a request-scoped context.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

// NewVerifiers builds the verifiers that cfg has credentials for. A platform
// with missing or unusable credentials is left out with a warning rather than
// failing startup, so a dev machine without store credentials still boots (its
// purchases just always fail verification).
func NewVerifiers(ctx context.Context, cfg *config.Config) Verifiers {
	vs := Verifiers{}

	if cfg.AppleSharedSecret != "" {
		vs[PlatformAppStore] = NewAppleVerifier(cfg.AppleSharedSecret, cfg.AppleBundleID)
		log.Printf("purchases: App Store verifier enabled (bundle_id check=%t)", cfg.AppleBundleID != "")
	} else {
		log.Println("WARNING: purchases: App Store verifier disabled (APPLE_SHARED_SECRET is not set); App Store purchases will be rejected")
	}

	switch {
	case cfg.GooglePackageName == "" || cfg.GoogleServiceAccountJSON == "":
		log.Println("WARNING: purchases: Google Play verifier disabled (GOOGLE_PACKAGE_NAME/GOOGLE_SERVICE_ACCOUNT_JSON are not set); Google Play purchases will be rejected")
	default:
		gv, err := NewGoogleVerifier(ctx, cfg.GooglePackageName, cfg.GoogleServiceAccountJSON)
		if err != nil {
			log.Printf("WARNING: purchases: Google Play verifier disabled: %v", err)
			break
		}
		vs[PlatformGooglePlay] = gv
		log.Printf("purchases: Google Play verifier enabled (package=%s)", cfg.GooglePackageName)
	}

	return vs
}

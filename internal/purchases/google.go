package purchases

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// androidPublisherScope is the only OAuth2 scope the Play Developer API needs
// for reading purchase state.
const androidPublisherScope = "https://www.googleapis.com/auth/androidpublisher"

const googleAPIBaseURL = "https://androidpublisher.googleapis.com"

// googlePurchaseStatePurchased is purchaseState 0; 1 is cancelled and 2 is
// pending (payment not completed), neither of which may be credited.
const googlePurchaseStatePurchased = 0

// GoogleVerifier validates Google Play purchases via the Play Developer API's
// purchases.products.get, authenticated with a service-account JWT.
type GoogleVerifier struct {
	packageName string
	tokens      oauth2.TokenSource
	baseURL     string
	http        *http.Client
}

// NewGoogleVerifier builds a verifier for the given Android applicationId.
// serviceAccount is either the raw service-account key JSON (detected by a
// leading "{") or a path to a file containing it.
//
// ctx bounds the lifetime of the token source, not a single verification, so it
// should be the process-level context.
func NewGoogleVerifier(ctx context.Context, packageName, serviceAccount string) (*GoogleVerifier, error) {
	key, err := readServiceAccountKey(serviceAccount)
	if err != nil {
		return nil, err
	}
	jwtCfg, err := google.JWTConfigFromJSON(key, androidPublisherScope)
	if err != nil {
		return nil, fmt.Errorf("parse google service account key: %w", err)
	}
	return &GoogleVerifier{
		packageName: packageName,
		tokens:      jwtCfg.TokenSource(ctx), // caches and refreshes access tokens
		baseURL:     googleAPIBaseURL,
		http:        newHTTPClient(),
	}, nil
}

// readServiceAccountKey resolves the configured value to key bytes.
func readServiceAccountKey(serviceAccount string) ([]byte, error) {
	if strings.HasPrefix(strings.TrimSpace(serviceAccount), "{") {
		return []byte(serviceAccount), nil
	}
	key, err := os.ReadFile(serviceAccount)
	if err != nil {
		return nil, fmt.Errorf("read google service account key file: %w", err)
	}
	return key, nil
}

// googleProductPurchase is the subset of ProductPurchase we act on.
type googleProductPurchase struct {
	PurchaseState int    `json:"purchaseState"`
	OrderID       string `json:"orderId"`
}

// Verify confirms with Google Play that verificationData (a purchase token) is a
// completed purchase of productID. The token itself is the transaction id --
// Play does not issue a separate one for in-app products.
func (v *GoogleVerifier) Verify(ctx context.Context, productID, verificationData string) (VerifiedPurchase, error) {
	if verificationData == "" {
		return VerifiedPurchase{}, fmt.Errorf("%w: empty purchase token", ErrNotVerified)
	}

	tok, err := v.tokens.Token()
	if err != nil {
		return VerifiedPurchase{}, fmt.Errorf("mint google access token: %w", err)
	}

	endpoint := fmt.Sprintf("%s/androidpublisher/v3/applications/%s/purchases/products/%s/tokens/%s",
		v.baseURL, url.PathEscape(v.packageName), url.PathEscape(productID), url.PathEscape(verificationData))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return VerifiedPurchase{}, fmt.Errorf("build play api request: %w", err)
	}
	tok.SetAuthHeader(req)

	resp, err := v.http.Do(req)
	if err != nil {
		log.Printf("[DEBUG] GoogleVerifier: play api call failed: %v", err)
		return VerifiedPurchase{}, fmt.Errorf("call play api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
		// fall through to body handling below
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// Our credentials are wrong or lack Play Console access -- an operator
		// problem, not a fake purchase.
		return VerifiedPurchase{}, fmt.Errorf("play api rejected our credentials: HTTP %d", resp.StatusCode)
	case resp.StatusCode >= 500:
		return VerifiedPurchase{}, fmt.Errorf("play api unavailable: HTTP %d", resp.StatusCode)
	default:
		// 400/404/410: no such token for this package+product.
		return VerifiedPurchase{}, fmt.Errorf("%w: play api HTTP %d", ErrNotVerified, resp.StatusCode)
	}

	var purchase googleProductPurchase
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&purchase); err != nil {
		return VerifiedPurchase{}, fmt.Errorf("decode play api response: %w", err)
	}
	if purchase.PurchaseState != googlePurchaseStatePurchased {
		return VerifiedPurchase{}, fmt.Errorf("%w: purchaseState=%d", ErrNotVerified, purchase.PurchaseState)
	}

	log.Printf("[DEBUG] GoogleVerifier: verified product=%s order_id=%s", productID, purchase.OrderID)
	return VerifiedPurchase{TransactionID: verificationData}, nil
}

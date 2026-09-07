package purchases

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
)

// Apple verifyReceipt endpoints. Production is tried first; a 21007 reply means
// the receipt came from the sandbox, so the same call is retried there. Apple
// documents this order because a single binary (TestFlight / App Review) can
// produce either kind of receipt.
const (
	appleProdURL    = "https://buy.itunes.apple.com/verifyReceipt"
	appleSandboxURL = "https://sandbox.itunes.apple.com/verifyReceipt"
)

const (
	appleStatusOK             = 0
	appleStatusSandboxReceipt = 21007
)

// AppleVerifier validates App Store receipts with the legacy verifyReceipt
// endpoint, which is the only one that works with a shared secret alone (the
// App Store Server API additionally needs a signing key + issuer id).
type AppleVerifier struct {
	sharedSecret string
	bundleID     string
	prodURL      string
	sandboxURL   string
	http         *http.Client
}

// NewAppleVerifier builds a verifier for the given app-specific shared secret.
// An empty bundleID skips the receipt bundle_id check.
func NewAppleVerifier(sharedSecret, bundleID string) *AppleVerifier {
	return &AppleVerifier{
		sharedSecret: sharedSecret,
		bundleID:     bundleID,
		prodURL:      appleProdURL,
		sandboxURL:   appleSandboxURL,
		http:         newHTTPClient(),
	}
}

type appleVerifyRequest struct {
	ReceiptData string `json:"receipt-data"`
	Password    string `json:"password"`
}

// appleInApp is one purchase entry inside a receipt.
type appleInApp struct {
	ProductID      string `json:"product_id"`
	TransactionID  string `json:"transaction_id"`
	PurchaseDateMS string `json:"purchase_date_ms"`
}

type appleVerifyResponse struct {
	Status  int `json:"status"`
	Receipt struct {
		BundleID string       `json:"bundle_id"`
		InApp    []appleInApp `json:"in_app"`
		// Very old receipt formats describe a single transaction at this level
		// instead of in an in_app array.
		ProductID      string `json:"product_id"`
		TransactionID  string `json:"transaction_id"`
		PurchaseDateMS string `json:"purchase_date_ms"`
	} `json:"receipt"`
	// LatestReceiptInfo is populated for auto-renewables but is also a useful
	// fallback when a consumable has already been dropped from in_app.
	LatestReceiptInfo []appleInApp `json:"latest_receipt_info"`
}

// Verify confirms with Apple that verificationData (a base64 app receipt)
// contains a purchase of productID, and returns that purchase's transaction id.
func (v *AppleVerifier) Verify(ctx context.Context, productID, verificationData string) (VerifiedPurchase, error) {
	if verificationData == "" {
		return VerifiedPurchase{}, fmt.Errorf("%w: empty receipt", ErrNotVerified)
	}

	res, err := v.post(ctx, v.prodURL, verificationData)
	if err != nil {
		return VerifiedPurchase{}, err
	}
	if res.Status == appleStatusSandboxReceipt {
		log.Printf("[DEBUG] AppleVerifier: production returned 21007, retrying against sandbox product=%s", productID)
		if res, err = v.post(ctx, v.sandboxURL, verificationData); err != nil {
			return VerifiedPurchase{}, err
		}
	}
	if res.Status != appleStatusOK {
		return VerifiedPurchase{}, fmt.Errorf("%w: apple verifyReceipt status %d", ErrNotVerified, res.Status)
	}
	if v.bundleID != "" && res.Receipt.BundleID != v.bundleID {
		// A receipt from a different app must never credit points here.
		return VerifiedPurchase{}, fmt.Errorf("%w: receipt bundle_id mismatch", ErrNotVerified)
	}

	txID := latestTransactionID(productID, res)
	if txID == "" {
		return VerifiedPurchase{}, fmt.Errorf("%w: no %q purchase in receipt", ErrNotVerified, productID)
	}
	log.Printf("[DEBUG] AppleVerifier: verified product=%s transaction_id=%s", productID, txID)
	return VerifiedPurchase{TransactionID: txID}, nil
}

// post sends one verifyReceipt request.
func (v *AppleVerifier) post(ctx context.Context, url, receipt string) (appleVerifyResponse, error) {
	body, err := json.Marshal(appleVerifyRequest{ReceiptData: receipt, Password: v.sharedSecret})
	if err != nil {
		return appleVerifyResponse{}, fmt.Errorf("encode verifyReceipt request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return appleVerifyResponse{}, fmt.Errorf("build verifyReceipt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.http.Do(req)
	if err != nil {
		log.Printf("[DEBUG] AppleVerifier: verifyReceipt call failed: %v", err)
		return appleVerifyResponse{}, fmt.Errorf("call verifyReceipt: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return appleVerifyResponse{}, fmt.Errorf("verifyReceipt: unexpected HTTP status %d", resp.StatusCode)
	}
	var out appleVerifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return appleVerifyResponse{}, fmt.Errorf("decode verifyReceipt response: %w", err)
	}
	return out, nil
}

// latestTransactionID picks the most recent transaction for productID across
// every place Apple may list it. Most recent wins because the client calls us
// right after buying: for a repeatable consumable the receipt can still hold
// earlier, already-credited transactions of the same product.
//
// TODO(iap): a receipt can legitimately carry several *uncredited* transactions
// of the same consumable (e.g. two buys while offline). Only the newest is
// credited here; crediting all of them needs Verify to return a list.
func latestTransactionID(productID string, res appleVerifyResponse) string {
	var (
		bestID string
		bestMS int64 = -1
	)
	consider := func(e appleInApp) {
		if e.ProductID != productID || e.TransactionID == "" {
			return
		}
		ms, err := strconv.ParseInt(e.PurchaseDateMS, 10, 64)
		if err != nil {
			ms = 0 // undated entries lose to any dated one but still count
		}
		if ms > bestMS {
			bestID, bestMS = e.TransactionID, ms
		}
	}

	for _, e := range res.Receipt.InApp {
		consider(e)
	}
	for _, e := range res.LatestReceiptInfo {
		consider(e)
	}
	consider(appleInApp{
		ProductID:      res.Receipt.ProductID,
		TransactionID:  res.Receipt.TransactionID,
		PurchaseDateMS: res.Receipt.PurchaseDateMS,
	})
	return bestID
}

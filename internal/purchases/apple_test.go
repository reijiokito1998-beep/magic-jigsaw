package purchases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// appleFake serves a canned verifyReceipt reply. Every decoded request is handed
// over on the returned channel, which also synchronises the handler goroutine
// with the test one.
func appleFake(t *testing.T, status int, payload string) (*httptest.Server, <-chan appleVerifyRequest) {
	t.Helper()
	reqs := make(chan appleVerifyRequest, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("verifyReceipt: got method %s, want POST", r.Method)
		}
		var req appleVerifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("verifyReceipt: decode body: %v", err)
			return
		}
		select {
		case reqs <- req:
		default:
			t.Error("verifyReceipt: more requests than expected")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, payload); err != nil {
			t.Errorf("verifyReceipt: write body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

// newTestAppleVerifier points a verifier at fakes instead of Apple.
func newTestAppleVerifier(prodURL, sandboxURL, bundleID string) *AppleVerifier {
	return &AppleVerifier{
		sharedSecret: "shhh",
		bundleID:     bundleID,
		prodURL:      prodURL,
		sandboxURL:   sandboxURL,
		http:         http.DefaultClient,
	}
}

func TestAppleVerify_Success(t *testing.T) {
	srv, reqs := appleFake(t, http.StatusOK, `{
		"status": 0,
		"receipt": {
			"bundle_id": "com.example.jigsaw",
			"in_app": [
				{"product_id": "other_product", "transaction_id": "tx-other", "purchase_date_ms": "1700000000000"},
				{"product_id": "unlock_points_50", "transaction_id": "tx-1", "purchase_date_ms": "1700000000000"}
			]
		}
	}`)

	v := newTestAppleVerifier(srv.URL, srv.URL, "com.example.jigsaw")
	got, err := v.Verify(context.Background(), "unlock_points_50", "base64-receipt")
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.TransactionID != "tx-1" {
		t.Errorf("TransactionID = %q, want %q", got.TransactionID, "tx-1")
	}

	sent := <-reqs
	if sent.ReceiptData != "base64-receipt" {
		t.Errorf("receipt-data = %q, want %q", sent.ReceiptData, "base64-receipt")
	}
	if sent.Password != "shhh" {
		t.Errorf("password = %q, want the shared secret", sent.Password)
	}
}

// A repeatable consumable leaves older transactions in the receipt; the newest
// one is the purchase the client just made.
func TestAppleVerify_PicksNewestTransaction(t *testing.T) {
	srv, _ := appleFake(t, http.StatusOK, `{
		"status": 0,
		"receipt": {
			"bundle_id": "com.example.jigsaw",
			"in_app": [
				{"product_id": "unlock_points_50", "transaction_id": "tx-old", "purchase_date_ms": "1700000000000"},
				{"product_id": "unlock_points_50", "transaction_id": "tx-new", "purchase_date_ms": "1800000000000"}
			]
		}
	}`)

	v := newTestAppleVerifier(srv.URL, srv.URL, "")
	got, err := v.Verify(context.Background(), "unlock_points_50", "r")
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.TransactionID != "tx-new" {
		t.Errorf("TransactionID = %q, want %q", got.TransactionID, "tx-new")
	}
}

// Old single-transaction receipts carry the fields on receipt itself.
func TestAppleVerify_LegacyTopLevelReceipt(t *testing.T) {
	srv, _ := appleFake(t, http.StatusOK, `{
		"status": 0,
		"receipt": {
			"bundle_id": "com.example.jigsaw",
			"product_id": "unlock_points_50",
			"transaction_id": "tx-legacy"
		}
	}`)

	v := newTestAppleVerifier(srv.URL, srv.URL, "")
	got, err := v.Verify(context.Background(), "unlock_points_50", "r")
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.TransactionID != "tx-legacy" {
		t.Errorf("TransactionID = %q, want %q", got.TransactionID, "tx-legacy")
	}
}

func TestAppleVerify_LatestReceiptInfoFallback(t *testing.T) {
	srv, _ := appleFake(t, http.StatusOK, `{
		"status": 0,
		"receipt": {"bundle_id": "com.example.jigsaw", "in_app": []},
		"latest_receipt_info": [
			{"product_id": "unlock_points_50", "transaction_id": "tx-latest", "purchase_date_ms": "1800000000000"}
		]
	}`)

	v := newTestAppleVerifier(srv.URL, srv.URL, "")
	got, err := v.Verify(context.Background(), "unlock_points_50", "r")
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.TransactionID != "tx-latest" {
		t.Errorf("TransactionID = %q, want %q", got.TransactionID, "tx-latest")
	}
}

// Status 21007 means "sandbox receipt sent to production"; the same call must be
// retried against the sandbox endpoint.
func TestAppleVerify_RetriesSandboxOn21007(t *testing.T) {
	prod, prodReqs := appleFake(t, http.StatusOK, `{"status": 21007}`)
	sandbox, sandboxReqs := appleFake(t, http.StatusOK, `{
		"status": 0,
		"receipt": {
			"bundle_id": "com.example.jigsaw",
			"in_app": [{"product_id": "unlock_points_50", "transaction_id": "tx-sandbox", "purchase_date_ms": "1"}]
		}
	}`)

	v := newTestAppleVerifier(prod.URL, sandbox.URL, "com.example.jigsaw")
	got, err := v.Verify(context.Background(), "unlock_points_50", "r")
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.TransactionID != "tx-sandbox" {
		t.Errorf("TransactionID = %q, want %q", got.TransactionID, "tx-sandbox")
	}
	if n := len(prodReqs); n != 1 {
		t.Errorf("production calls = %d, want 1", n)
	}
	if n := len(sandboxReqs); n != 1 {
		t.Errorf("sandbox calls = %d, want 1", n)
	}
}

func TestAppleVerify_NotVerified(t *testing.T) {
	tests := []struct {
		name     string
		bundleID string
		reply    string
	}{
		{
			name:  "malformed receipt status",
			reply: `{"status": 21002}`,
		},
		{
			name:  "product not in receipt",
			reply: `{"status": 0, "receipt": {"in_app": [{"product_id": "something_else", "transaction_id": "tx-x"}]}}`,
		},
		{
			name:  "matching product without transaction id",
			reply: `{"status": 0, "receipt": {"in_app": [{"product_id": "unlock_points_50"}]}}`,
		},
		{
			name:     "receipt from another app",
			bundleID: "com.example.jigsaw",
			reply: `{"status": 0, "receipt": {"bundle_id": "com.attacker.app",
				"in_app": [{"product_id": "unlock_points_50", "transaction_id": "tx-1"}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := appleFake(t, http.StatusOK, tt.reply)
			v := newTestAppleVerifier(srv.URL, srv.URL, tt.bundleID)
			if _, err := v.Verify(context.Background(), "unlock_points_50", "r"); !errors.Is(err, ErrNotVerified) {
				t.Fatalf("Verify: error = %v, want ErrNotVerified", err)
			}
		})
	}
}

func TestAppleVerify_EmptyReceiptSkipsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Verify called Apple for an empty receipt")
	}))
	t.Cleanup(srv.Close)

	v := newTestAppleVerifier(srv.URL, srv.URL, "")
	if _, err := v.Verify(context.Background(), "unlock_points_50", ""); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("Verify: error = %v, want ErrNotVerified", err)
	}
}

// A broken endpoint is an infrastructure failure, not proof of a fake purchase:
// it must not wrap ErrNotVerified, so the client is told to retry.
func TestAppleVerify_HTTPErrorIsNotAVerdict(t *testing.T) {
	srv, _ := appleFake(t, http.StatusInternalServerError, `{}`)
	v := newTestAppleVerifier(srv.URL, srv.URL, "")
	_, err := v.Verify(context.Background(), "unlock_points_50", "r")
	if err == nil {
		t.Fatal("Verify: expected an error")
	}
	if errors.Is(err, ErrNotVerified) {
		t.Errorf("Verify: error = %v, want a transient error, not ErrNotVerified", err)
	}
}

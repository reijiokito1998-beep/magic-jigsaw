package purchases

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

const (
	testPackageName = "com.example.jigsaw"
	testProductID   = "unlock_points_50"
	testPurchaseTok = "tok-abc123"
	testAccessToken = "test-access-token"
	testWantPath    = "/androidpublisher/v3/applications/" + testPackageName +
		"/purchases/products/" + testProductID + "/tokens/" + testPurchaseTok
)

// playFake serves one canned purchases.products.get reply. The received request
// is handed over on the returned channel, which also synchronises the handler
// goroutine with the test one.
func playFake(t *testing.T, status int, payload string) (*httptest.Server, <-chan *http.Request) {
	t.Helper()
	reqs := make(chan *http.Request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reqs <- r.Clone(context.Background()):
		default:
			t.Error("play api fake: more requests than expected")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, payload); err != nil {
			t.Errorf("play api fake: write body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

// newTestGoogleVerifier points a verifier at a fake API with a static token, so
// no service-account key or real token exchange is needed.
func newTestGoogleVerifier(baseURL string) *GoogleVerifier {
	return &GoogleVerifier{
		packageName: testPackageName,
		tokens:      oauth2.StaticTokenSource(&oauth2.Token{AccessToken: testAccessToken, TokenType: "Bearer"}),
		baseURL:     baseURL,
		http:        http.DefaultClient,
	}
}

func TestGoogleVerify_Success(t *testing.T) {
	srv, reqs := playFake(t, http.StatusOK, `{"purchaseState": 0, "orderId": "GPA.1234"}`)

	v := newTestGoogleVerifier(srv.URL)
	verified, err := v.Verify(context.Background(), testProductID, testPurchaseTok)
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	// Play has no separate transaction id, so the token is the idempotency key.
	if verified.TransactionID != testPurchaseTok {
		t.Errorf("TransactionID = %q, want %q", verified.TransactionID, testPurchaseTok)
	}

	got := <-reqs
	if got.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.Method)
	}
	if got.URL.Path != testWantPath {
		t.Errorf("path = %q, want %q", got.URL.Path, testWantPath)
	}
	if want := "Bearer " + testAccessToken; got.Header.Get("Authorization") != want {
		t.Errorf("Authorization = %q, want %q", got.Header.Get("Authorization"), want)
	}
}

func TestGoogleVerify_NotVerified(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		payload string
	}{
		{name: "cancelled purchase", status: http.StatusOK, payload: `{"purchaseState": 1}`},
		{name: "pending purchase", status: http.StatusOK, payload: `{"purchaseState": 2}`},
		{name: "token not found", status: http.StatusNotFound, payload: `{"error": {"code": 404}}`},
		{name: "bad token", status: http.StatusBadRequest, payload: `{"error": {"code": 400}}`},
		{name: "token gone", status: http.StatusGone, payload: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := playFake(t, tt.status, tt.payload)
			v := newTestGoogleVerifier(srv.URL)
			if _, err := v.Verify(context.Background(), testProductID, testPurchaseTok); !errors.Is(err, ErrNotVerified) {
				t.Fatalf("Verify: error = %v, want ErrNotVerified", err)
			}
		})
	}
}

// Credential and outage failures say nothing about the purchase, so they must
// not wrap ErrNotVerified -- the handler tells the client to retry those.
func TestGoogleVerify_TransientErrorsAreNotAVerdict(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "forbidden", status: http.StatusForbidden},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "unavailable", status: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := playFake(t, tt.status, `{}`)
			v := newTestGoogleVerifier(srv.URL)
			_, err := v.Verify(context.Background(), testProductID, testPurchaseTok)
			if err == nil {
				t.Fatal("Verify: expected an error")
			}
			if errors.Is(err, ErrNotVerified) {
				t.Errorf("Verify: error = %v, want a transient error, not ErrNotVerified", err)
			}
		})
	}
}

func TestGoogleVerify_EmptyTokenSkipsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Verify called the Play API for an empty purchase token")
	}))
	t.Cleanup(srv.Close)

	v := newTestGoogleVerifier(srv.URL)
	if _, err := v.Verify(context.Background(), testProductID, ""); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("Verify: error = %v, want ErrNotVerified", err)
	}
}

func TestReadServiceAccountKey(t *testing.T) {
	const raw = `{"type": "service_account", "client_email": "a@b.iam.gserviceaccount.com"}`

	t.Run("raw json", func(t *testing.T) {
		// Leading whitespace still counts as inline JSON, not a file path.
		got, err := readServiceAccountKey("  " + raw)
		if err != nil {
			t.Fatalf("readServiceAccountKey: unexpected error: %v", err)
		}
		if string(got) != "  "+raw {
			t.Errorf("got %q, want the raw JSON unchanged", got)
		}
	})

	t.Run("file path", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "key.json")
		if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
			t.Fatalf("write key file: %v", err)
		}
		got, err := readServiceAccountKey(file)
		if err != nil {
			t.Fatalf("readServiceAccountKey: unexpected error: %v", err)
		}
		if string(got) != raw {
			t.Errorf("got %q, want %q", got, raw)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := readServiceAccountKey(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("readServiceAccountKey: expected an error for a missing file")
		}
	})
}

func TestRewardFor(t *testing.T) {
	cases := []struct {
		productID string
		want      Reward
		wantOK    bool
	}{
		{"unlock_points_50", Reward{Points: 50}, true},
		{"unlock_points_20", Reward{Points: 20}, true},
		{"expedition_stars_50", Reward{ExpeditionStars: 50}, true},
		// An unknown (or client-invented) product must never be creditable.
		{"free_points_9999", Reward{}, false},
	}
	for _, c := range cases {
		got, ok := RewardFor(c.productID)
		if ok != c.wantOK || got != c.want {
			t.Errorf("RewardFor(%q) = %+v, %t; want %+v, %t", c.productID, got, ok, c.want, c.wantOK)
		}
	}
}

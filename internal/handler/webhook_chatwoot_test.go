package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

func TestChatwootHMAC401(t *testing.T) {
	h := &ChatwootWebhook{
		Tenants: &stubLookuper{resolved: &tenant.Resolved{
			ID: uuid.New(), ChatwootHMACSecret: "topsecret",
		}},
		Enqueuer: nopEnqueuer{},
	}
	r := chi.NewRouter()
	r.Post("/v1/cw/{slug}", h.ServeHTTP)

	body := []byte(`{"event":"message_created","id":1,"message_type":"outgoing","conversation":{"id":1}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", "deadbeef")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestChatwootHMACSignedSucceedsThroughAuth(t *testing.T) {
	// We can't fully run the handler without DB, but we can confirm the HMAC
	// gate accepts a valid signature: handler will then reach Queries (nil) and
	// either skip (non-actionable event) or fail. The test asserts non-401.
	secret := "abc"
	body := []byte(`{"event":"unknown_event"}`)
	sig := crypto.SignHMAC(body, secret)

	h := &ChatwootWebhook{
		Tenants: &stubLookuper{resolved: &tenant.Resolved{
			ID: uuid.New(), ChatwootHMACSecret: secret,
		}},
		Enqueuer: nopEnqueuer{},
	}
	r := chi.NewRouter()
	r.Post("/v1/cw/{slug}", h.ServeHTTP)

	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", sig)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("HMAC accepted signature should not produce 401")
	}
}

// TestChatwootHubSignature256HeaderAccepted verifies that the GitHub-style
// X-Hub-Signature-256 fallback header is accepted: the "sha256=" prefix must
// be stripped before length-comparing against the raw-hex HMAC. Prior to the
// fix, all such payloads returned 401 because the prefix made the length differ.
func TestChatwootHubSignature256HeaderAccepted(t *testing.T) {
	secret := "abc"
	body := []byte(`{"event":"unknown_event"}`)
	sig := crypto.SignHMAC(body, secret)

	h := &ChatwootWebhook{
		Tenants: &stubLookuper{resolved: &tenant.Resolved{
			ID: uuid.New(), ChatwootHMACSecret: secret,
		}},
		Enqueuer: nopEnqueuer{},
	}
	r := chi.NewRouter()
	r.Post("/v1/cw/{slug}", h.ServeHTTP)

	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+sig)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("X-Hub-Signature-256 with sha256= prefix should not produce 401, got %d", rec.Code)
	}
}

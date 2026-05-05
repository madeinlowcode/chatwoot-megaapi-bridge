package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

type stubLookuper struct {
	resolved *tenant.Resolved
	err      error
}

func (s *stubLookuper) Lookup(_ context.Context, slug string) (*tenant.Resolved, error) {
	if s.err != nil {
		return nil, s.err
	}
	r := *s.resolved
	r.Slug = slug
	return &r, nil
}

func (s *stubLookuper) LookupByID(_ context.Context, id uuid.UUID) (*tenant.Resolved, error) {
	if s.err != nil {
		return nil, s.err
	}
	r := *s.resolved
	r.ID = id
	return &r, nil
}

func (s *stubLookuper) Invalidate(_ string) {}

// Note: full DB-backed flow is exercised by the e2e test (task 15) and worker
// tests. Unit tests here cover the auth path (no DB needed).

func TestMegaapiAuth401(t *testing.T) {
	h := &MegaapiWebhook{
		Tenants: &stubLookuper{resolved: &tenant.Resolved{
			ID: uuid.New(), MegaapiWebhookBearer: "right-token",
		}},
		Queries:  nil, // unreachable for this path
		Enqueuer: nopEnqueuer{},
	}

	r := chi.NewRouter()
	r.Post("/v1/wa/{slug}", h.ServeHTTP)

	body, _ := json.Marshal(map[string]any{"messages": []any{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

type nopEnqueuer struct{}

func (nopEnqueuer) EnqueueWAtoCW(context.Context, queue.WAtoCWPayload) error { return nil }
func (nopEnqueuer) EnqueueCWtoWA(context.Context, queue.CWtoWAPayload) error { return nil }

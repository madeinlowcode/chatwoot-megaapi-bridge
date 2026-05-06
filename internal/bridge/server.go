package bridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	maxBodyBytes  = 1 << 20
	directionIn   = "in"
	directionOut  = "out"
	hmacHeader    = "X-Chatwoot-Signature"
	bearerPrefix  = "Bearer "
	defaultBuffer = 1000
)

type Config struct {
	BufferLimit int
	Workers     int
}

type Server struct {
	DB     *DB
	Key    []byte
	Inbox  chan Job
	Outbox chan Job
	Log    zerolog.Logger
	Cfg    Config
}

func NewServer(db *DB, key []byte, cfg Config, log zerolog.Logger) *Server {
	if cfg.BufferLimit <= 0 {
		cfg.BufferLimit = defaultBuffer
	}
	return &Server{
		DB:     db,
		Key:    key,
		Inbox:  make(chan Job, cfg.BufferLimit),
		Outbox: make(chan Job, cfg.BufferLimit),
		Log:    log,
		Cfg:    cfg,
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Post("/v1/wa/{slug}", s.handleWAWebhook)
	r.Post("/v1/cw/{slug}", s.handleCWWebhook)
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"db": "down"})
		return
	}
	if len(s.Inbox) >= s.Cfg.BufferLimit || len(s.Outbox) >= s.Cfg.BufferLimit {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"queue": "full"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWAWebhook(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.lookupTenant(w, r)
	if !ok {
		return
	}
	authed, err := s.checkBearer(r, tenant)
	if err != nil {
		s.Log.Err(err).Str("tenant_id", tenant.ID.String()).Msg("decrypt webhook bearer")
		http.Error(w, "crypto error", http.StatusInternalServerError)
		return
	}
	if !authed {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, ok := readBodyOr400(w, r)
	if !ok {
		return
	}
	extID, ok := extractWAExternalID(body)
	if !ok {
		http.Error(w, "missing external id", http.StatusBadRequest)
		return
	}
	s.enqueue(w, r.Context(), tenant.ID, directionIn, extID, body, s.Inbox)
}

func (s *Server) handleCWWebhook(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.lookupTenant(w, r)
	if !ok {
		return
	}
	body, ok := readBodyOr400(w, r)
	if !ok {
		return
	}
	authed, err := s.checkHMAC(r, tenant, body)
	if err != nil {
		s.Log.Err(err).Str("tenant_id", tenant.ID.String()).Msg("decrypt hmac secret")
		http.Error(w, "crypto error", http.StatusInternalServerError)
		return
	}
	if !authed {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !chatwootShouldRelay(body) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	extID, ok := extractCWExternalID(body)
	if !ok {
		http.Error(w, "missing external id", http.StatusBadRequest)
		return
	}
	s.enqueue(w, r.Context(), tenant.ID, directionOut, extID, body, s.Outbox)
}

func (s *Server) lookupTenant(w http.ResponseWriter, r *http.Request) (Tenant, bool) {
	slug := chi.URLParam(r, "slug")
	t, err := s.DB.GetTenantBySlug(r.Context(), slug)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return Tenant{}, false
	}
	if err != nil {
		s.Log.Err(err).Msg("get tenant")
		http.Error(w, "db error", http.StatusInternalServerError)
		return Tenant{}, false
	}
	return t, true
}

func readBodyOr400(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func (s *Server) enqueue(w http.ResponseWriter, ctx context.Context, tenantID uuid.UUID,
	direction, extID string, body []byte, ch chan Job) {
	id, created, err := s.DB.InsertMessage(ctx, Message{
		TenantID: tenantID, Direction: direction, ExternalID: extID, Payload: body,
	})
	if err != nil {
		s.Log.Err(err).Msg("insert message")
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	select {
	case ch <- Job{TenantID: tenantID, MessageID: id, Payload: body}:
		writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
	default:
		_ = s.DB.MarkStatus(ctx, id, "failed", "queue full")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"queue": "full"})
	}
}

func (s *Server) checkBearer(r *http.Request, t Tenant) (bool, error) {
	tok, err := Decrypt(t.WebhookBearerEnc, s.Key)
	if err != nil {
		return false, err
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), bearerPrefix)
	if got == "" {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(got), tok) == 1, nil
}

func (s *Server) checkHMAC(r *http.Request, t Tenant, body []byte) (bool, error) {
	secret, err := Decrypt(t.HMACSecretEnc, s.Key)
	if err != nil {
		return false, err
	}
	return VerifyHMAC(body, r.Header.Get(hmacHeader), string(secret)), nil
}

func readBody(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	return io.ReadAll(r.Body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

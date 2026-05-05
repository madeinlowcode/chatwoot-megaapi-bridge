package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

// chatwootWebhookEvent matches the relevant fields of Chatwoot's outgoing webhook.
type chatwootWebhookEvent struct {
	Event        string `json:"event"`
	ID           int64  `json:"id"`
	Content      string `json:"content"`
	MessageType  any    `json:"message_type"` // sometimes int, sometimes string
	Private      bool   `json:"private"`
	Conversation struct {
		ID           int64 `json:"id"`
		ContactInbox struct {
			SourceID string `json:"source_id"`
		} `json:"contact_inbox"`
	} `json:"conversation"`
}

func (e chatwootWebhookEvent) isOutgoing() bool {
	switch v := e.MessageType.(type) {
	case string:
		return v == "outgoing" || v == "1"
	case float64:
		return v == 1
	}
	return false
}

// ChatwootWebhook handles outgoing-message webhooks from Chatwoot.
type ChatwootWebhook struct {
	Tenants  tenant.Lookuper
	Queries  *repo.Queries
	Enqueuer interface {
		EnqueueCWtoWA(ctx context.Context, p queue.CWtoWAPayload) error
	}
	MaxBody int64
}

func (h *ChatwootWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "missing tenant slug")
		return
	}
	ctx = observability.WithTenant(ctx, slug)
	r = r.WithContext(ctx)
	log := observability.FromContext(ctx)

	t, err := h.Tenants.Lookup(ctx, slug)
	if err != nil {
		if errors.Is(err, tenant.ErrNotFound) {
			log.Warn().Str("kind", "webhook.outbound.rejected.unknown_tenant").Msg("tenant not found")
			writeError(w, r, http.StatusNotFound, CodeTenantNotFound, "tenant not found")
			return
		}
		log.Error().Err(err).Str("kind", "webhook.outbound.rejected.lookup_error").Msg("lookup failed")
		writeError(w, r, http.StatusServiceUnavailable, CodeDependencyDown, "lookup unavailable")
		return
	}

	maxBody := h.MaxBody
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		log.Warn().Err(err).Str("kind", "webhook.outbound.rejected.body_read").Msg("read body")
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "read body")
		return
	}
	if int64(len(body)) > maxBody {
		writeError(w, r, http.StatusRequestEntityTooLarge, CodePayloadInvalid, "body too large")
		return
	}

	// HMAC validation. Chatwoot sends X-Chatwoot-Signature OR sometimes
	// X-Hub-Signature-256 (varies by version). We accept both.
	// X-Hub-Signature-256 is GitHub-style "sha256=<hex>" — strip the prefix
	// so it lines up with our raw-hex compare in crypto.VerifyHMAC.
	sig := r.Header.Get("X-Chatwoot-Signature")
	if sig == "" {
		sig = strings.TrimPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	}
	if !crypto.VerifyHMAC(body, sig, t.ChatwootHMACSecret) {
		log.Warn().Str("kind", "webhook.outbound.rejected.auth").Msg("hmac invalid")
		writeError(w, r, http.StatusUnauthorized, CodeAuthInvalid, "invalid signature")
		return
	}

	var ev chatwootWebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		log.Warn().Err(err).Str("kind", "webhook.outbound.rejected.parse").Msg("parse fail")
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "invalid json")
		return
	}

	if ev.Event != "message_created" || !ev.isOutgoing() || ev.Private {
		// not an actionable event, ACK quietly
		log.Debug().Str("kind", "webhook.outbound.skipped").Str("event", ev.Event).Msg("skipped")
		w.WriteHeader(http.StatusOK)
		return
	}
	if ev.ID == 0 {
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "missing message id")
		return
	}

	// Idempotency relies on messages.UNIQUE(tenant_id, direction, external_id)
	// surfaced as InsertMessageIfAbsent's (id, isNew, err) tuple. The separate
	// idempotency_keys table is reserved for future replay/admin scopes; using
	// it as a pre-flight guard here would commit a row before persist+enqueue
	// and silently swallow real messages on a transient blip after the commit.
	externalID := strconv.FormatInt(ev.ID, 10)
	msgID, isNew, err := h.Queries.InsertMessageIfAbsent(ctx, t.ID, repo.DirectionOutbound, externalID, body)
	if err != nil {
		log.Error().Err(err).Str("kind", "webhook.outbound.error.persist").Msg("persist fail")
		writeError(w, r, http.StatusServiceUnavailable, CodeDependencyDown, "db unavailable")
		return
	}
	if !isNew {
		log.Info().Str("external_id", externalID).Str("kind", "webhook.outbound.duplicate").Msg("duplicate")
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.Enqueuer.EnqueueCWtoWA(ctx, queue.CWtoWAPayload{
		TenantID:   t.ID,
		MessageID:  msgID,
		ExternalID: externalID,
	}); err != nil {
		log.Error().Err(err).Str("kind", "webhook.outbound.error.enqueue").Msg("enqueue")
		writeError(w, r, http.StatusServiceUnavailable, CodeQueueFull, "queue unavailable")
		return
	}

	log.Info().
		Str("external_id", externalID).
		Str("kind", "webhook.outbound.accepted").
		Msg("queued")

	tenantID := t.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.Queries.InsertAuditEvent(ctx, &tenantID, "webhook.outbound.received", true, nil)
	}()

	w.WriteHeader(http.StatusOK)
}

package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/megaapi"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

// MegaapiWebhook handles inbound webhooks from megaAPI.
type MegaapiWebhook struct {
	Tenants  tenant.Lookuper
	Queries  *repo.Queries
	Enqueuer interface {
		EnqueueWAtoCW(ctx context.Context, p queue.WAtoCWPayload) error
	}
	MaxBody int64
}

func (h *MegaapiWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
			log.Warn().Str("kind", "webhook.inbound.rejected.unknown_tenant").Msg("tenant not found")
			writeError(w, r, http.StatusNotFound, CodeTenantNotFound, "tenant not found")
			return
		}
		log.Error().Err(err).Str("kind", "webhook.inbound.rejected.lookup_error").Msg("lookup failed")
		writeError(w, r, http.StatusServiceUnavailable, CodeDependencyDown, "lookup unavailable")
		return
	}

	// Bearer auth
	auth := r.Header.Get("Authorization")
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(auth, bearerPrefix) ||
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, bearerPrefix)), []byte(t.MegaapiWebhookBearer)) != 1 {
		log.Warn().Str("kind", "webhook.inbound.rejected.auth").Msg("invalid bearer")
		writeError(w, r, http.StatusUnauthorized, CodeAuthInvalid, "invalid bearer")
		return
	}

	maxBody := h.MaxBody
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "read body")
		return
	}
	if int64(len(body)) > maxBody {
		writeError(w, r, http.StatusRequestEntityTooLarge, CodePayloadInvalid, "body too large")
		return
	}

	var payload megaapi.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Warn().Err(err).Str("kind", "webhook.inbound.rejected.parse").Msg("parse fail")
		writeError(w, r, http.StatusBadRequest, CodePayloadInvalid, "invalid json")
		return
	}

	processed := 0
	for _, m := range payload.Messages {
		if m.Key.FromMe {
			continue // we don't relay our own outbound echoes
		}
		text := strings.TrimSpace(m.Message.Conversation)
		if text == "" {
			// MVP: only text. Non-text messages are ignored (logged) until F2.
			log.Info().Str("kind", "webhook.inbound.skipped.non_text").Str("external_id", m.Key.ID).Msg("skipped")
			continue
		}
		if m.Key.ID == "" {
			log.Warn().Str("kind", "webhook.inbound.rejected.missing_external_id").Msg("missing key.id")
			continue
		}

		// Idempotency relies on messages.UNIQUE(tenant_id, direction, external_id)
		// surfaced as InsertMessageIfAbsent's (id, isNew, err) tuple. We deliberately
		// do not commit an idempotency_keys row before persist+enqueue: a transient
		// blip after that commit would cause the upstream retry to ACK 200 without
		// persisting/enqueueing, silently dropping a real user message.
		msgPayload, err := json.Marshal(m)
		if err != nil {
			log.Warn().Err(err).Str("kind", "webhook.inbound.rejected.marshal").Str("external_id", m.Key.ID).Msg("marshal fail")
			continue
		}
		msgID, isNew, err := h.Queries.InsertMessageIfAbsent(ctx, t.ID, repo.DirectionInbound, m.Key.ID, msgPayload)
		if err != nil {
			log.Error().Err(err).Str("kind", "webhook.inbound.error.persist").Msg("persist fail")
			writeError(w, r, http.StatusServiceUnavailable, CodeDependencyDown, "db unavailable")
			return
		}
		if !isNew {
			log.Info().Str("external_id", m.Key.ID).Str("kind", "webhook.inbound.duplicate").Msg("duplicate")
			continue
		}

		if err := h.Enqueuer.EnqueueWAtoCW(ctx, queue.WAtoCWPayload{
			TenantID:   t.ID,
			MessageID:  msgID,
			ExternalID: m.Key.ID,
		}); err != nil {
			log.Error().Err(err).Str("kind", "webhook.inbound.error.enqueue").Msg("enqueue fail")
			writeError(w, r, http.StatusServiceUnavailable, CodeQueueFull, "queue unavailable")
			return
		}
		processed++
		log.Info().
			Str("external_id", m.Key.ID).
			Str("kind", "webhook.inbound.accepted").
			Msg("queued")
	}
	_ = processed

	// audit (fire-and-forget)
	tenantID := t.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.Queries.InsertAuditEvent(ctx, &tenantID, "webhook.inbound.received", true, nil)
	}()

	w.WriteHeader(http.StatusOK)
}

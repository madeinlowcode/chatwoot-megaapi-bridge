package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/megaapi"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

// CWtoWA handles the Chatwoot-to-megaAPI outbound job.
type CWtoWA struct {
	Tenants tenant.Lookuper
	Queries *repo.Queries
	Megaapi megaapi.Client
}

// chatwootStored is the persisted webhook event we re-parse.
type chatwootStored struct {
	Content      string `json:"content"`
	Conversation struct {
		ID           int64 `json:"id"`
		ContactInbox struct {
			SourceID string `json:"source_id"`
		} `json:"contact_inbox"`
	} `json:"conversation"`
}

func (w *CWtoWA) HandleTask(ctx context.Context, t *asynq.Task) error {
	var p queue.CWtoWAPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("worker: invalid payload: %w: %w", err, asynq.SkipRetry)
	}

	msg, err := w.Queries.GetMessage(ctx, p.MessageID)
	if err != nil {
		return fmt.Errorf("worker: load message: %w", err)
	}

	resolved, err := w.Tenants.LookupByID(ctx, p.TenantID)
	if err != nil {
		return fmt.Errorf("worker: resolve tenant: %w", err)
	}
	ctx = observability.WithTenant(ctx, resolved.Slug)
	log := observability.FromContext(ctx)
	log.Info().Str("kind", "worker.send.started").Str("queue", queue.QueueCWtoWA).Msg("started")

	// Idempotency guard: cw_to_wa has no upstream id we persist, so there is
	// nothing analogous to wa_to_cw's CWMessageID-presence check. We do, however,
	// short-circuit any retry that finds the row already in 'delivered'.
	if msg.Status == repo.StatusDelivered {
		log.Info().Str("kind", "worker.send.skipped.already_delivered").Msg("already delivered")
		return nil
	}

	tryUpdateStatus(ctx, w.Queries, log, msg.ID, repo.StatusSending, nil, "sending")

	var ev chatwootStored
	if err := json.Unmarshal(msg.Payload, &ev); err != nil {
		errMsg := err.Error()
		tryUpdateStatus(ctx, w.Queries, log, msg.ID, repo.StatusFailed, &errMsg, "parse_payload_failed")
		return fmt.Errorf("worker: parse stored payload: %w: %w", err, asynq.SkipRetry)
	}
	to := ev.Conversation.ContactInbox.SourceID
	text := strings.TrimSpace(ev.Content)
	if to == "" || text == "" {
		errMsg := "missing destination or content"
		tryUpdateStatus(ctx, w.Queries, log, msg.ID, repo.StatusFailed, &errMsg, "missing_dest_or_content")
		return fmt.Errorf("worker: %s: %w", errMsg, asynq.SkipRetry)
	}

	if err := w.Megaapi.SendText(ctx, resolved.MegaapiHost, resolved.MegaapiInstanceKey, resolved.MegaapiBearerToken, to, text); err != nil {
		return classify(err, "megaapi.send_text")
	}

	// Surface delivery-write errors so asynq retries; the idempotency guard above
	// keeps the next attempt from re-sending to megaAPI on a transient DB blip.
	if err := w.Queries.MarkMessageDelivered(ctx, msg.ID); err != nil {
		log.Error().Err(err).Str("kind", "worker.db.error.set_delivered").Msg("mark delivered")
		return fmt.Errorf("worker: mark delivered: %w", err)
	}
	log.Info().
		Str("kind", "worker.send.succeeded").
		Str("queue", queue.QueueCWtoWA).
		Msg("delivered")
	return nil
}

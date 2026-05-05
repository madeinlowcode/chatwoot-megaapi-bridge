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

	_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusSending, nil)

	var ev chatwootStored
	if err := json.Unmarshal(msg.Payload, &ev); err != nil {
		errMsg := err.Error()
		_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusFailed, &errMsg)
		return fmt.Errorf("worker: parse stored payload: %w: %w", err, asynq.SkipRetry)
	}
	to := ev.Conversation.ContactInbox.SourceID
	text := strings.TrimSpace(ev.Content)
	if to == "" || text == "" {
		errMsg := "missing destination or content"
		_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusFailed, &errMsg)
		return fmt.Errorf("worker: %s: %w", errMsg, asynq.SkipRetry)
	}

	if err := w.Megaapi.SendText(ctx, resolved.MegaapiHost, resolved.MegaapiInstanceKey, resolved.MegaapiBearerToken, to, text); err != nil {
		return classify(err, "megaapi.send_text")
	}

	_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusDelivered, nil)
	log.Info().
		Str("kind", "worker.send.succeeded").
		Str("queue", queue.QueueCWtoWA).
		Msg("delivered")
	return nil
}

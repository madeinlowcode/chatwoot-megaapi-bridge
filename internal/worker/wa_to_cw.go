package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/chatwoot"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/megaapi"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
)

// WAtoCW handles the inbound-to-chatwoot job.
type WAtoCW struct {
	Tenants  tenant.Lookuper
	Queries  *repo.Queries
	Chatwoot chatwoot.Client
}

func (w *WAtoCW) HandleTask(ctx context.Context, t *asynq.Task) error {
	var p queue.WAtoCWPayload
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
	log.Info().Str("kind", "worker.send.started").Str("queue", queue.QueueWAtoCW).Msg("started")

	// Idempotency guard: if Chatwoot already accepted this message in a prior
	// attempt, just close out the DB-side state. Re-calling CreateMessage would
	// duplicate in Chatwoot since their dedup keys on our cw_message_id, not the
	// upstream WA external_id.
	if msg.CWMessageID != nil {
		if err := w.Queries.MarkMessageDelivered(ctx, msg.ID); err != nil {
			log.Error().Err(err).Str("kind", "worker.db.error.mark_delivered").Msg("mark delivered")
			return fmt.Errorf("worker: mark delivered: %w", err)
		}
		log.Info().Str("kind", "worker.send.skipped.duplicate_attempt").Int64("cw_message_id", *msg.CWMessageID).Msg("duplicate attempt; already delivered")
		return nil
	}

	tryUpdateStatus(ctx, w.Queries, log, msg.ID, repo.StatusSending, nil, "sending")

	var waMsg megaapi.WebhookMessage
	if err := json.Unmarshal(msg.Payload, &waMsg); err != nil {
		errMsg := err.Error()
		tryUpdateStatus(ctx, w.Queries, log, msg.ID, repo.StatusFailed, &errMsg, "parse_payload_failed")
		return fmt.Errorf("worker: parse stored payload: %w: %w", err, asynq.SkipRetry)
	}

	jid := waMsg.Key.RemoteJID
	phone := jidToPhone(jid)
	pushName := waMsg.PushName

	cwCfg := chatwoot.Config{
		BaseURL:         resolved.ChatwootBaseURL,
		APIToken:        resolved.ChatwootAPIToken,
		AccountID:       resolved.ChatwootAccountID,
		InboxID:         resolved.ChatwootInboxID,
		InboxIdentifier: resolved.ChatwootInboxIdentifier,
	}

	contact, err := w.Queries.GetContactByJID(ctx, resolved.ID, jid)
	var cwContactID int64
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	if errors.Is(err, repo.ErrNotFound) || contact == nil || contact.CWContactID == 0 {
		results, err := w.Chatwoot.SearchContact(ctx, cwCfg, phone)
		if err != nil {
			return classify(err, "chatwoot.search")
		}
		if len(results) > 0 {
			cwContactID = results[0].ID
		} else {
			created, err := w.Chatwoot.CreateContact(ctx, cwCfg, chatwoot.CreateContactRequest{
				Name:        nameOr(pushName, phone),
				PhoneNumber: phone,
				Identifier:  jid,
				InboxID:     resolved.ChatwootInboxID,
			})
			if err != nil {
				return classify(err, "chatwoot.create_contact")
			}
			cwContactID = created.ID
		}
		var dnPtr *string
		if pushName != "" {
			pn := pushName
			dnPtr = &pn
		}
		ct, err := w.Queries.UpsertContact(ctx, resolved.ID, jid, cwContactID, dnPtr)
		if err != nil {
			return err
		}
		contact = ct
	} else {
		cwContactID = contact.CWContactID
	}

	if err := w.Queries.SetMessageContact(ctx, msg.ID, contact.ID); err != nil {
		log.Error().Err(err).Str("kind", "worker.db.error.set_contact").Msg("set message contact")
	}

	var convoID int64
	if contact.CWConversationID != nil {
		convoID = *contact.CWConversationID
	}
	if convoID == 0 {
		conv, err := w.Chatwoot.CreateConversation(ctx, cwCfg, chatwoot.CreateConversationRequest{
			SourceID:  jid,
			InboxID:   resolved.ChatwootInboxID,
			ContactID: cwContactID,
			Status:    "open",
		})
		if err != nil {
			return classify(err, "chatwoot.create_conversation")
		}
		convoID = conv.ID
		if err := w.Queries.SetContactConversation(ctx, contact.ID, convoID); err != nil {
			log.Error().Err(err).Str("kind", "worker.db.error.set_conversation").Msg("set contact conversation")
		}
	}

	cwMsg, err := w.Chatwoot.CreateMessage(ctx, cwCfg, convoID, chatwoot.CreateMessageRequest{
		Content:     strings.TrimSpace(waMsg.Message.Conversation),
		MessageType: "incoming",
		Private:     false,
		ContentAttributes: map[string]any{
			"external_id": waMsg.Key.ID,
		},
	})
	if err != nil {
		return classify(err, "chatwoot.create_message")
	}

	// Atomic write: pinning cw_message_id and 'delivered' in one UPDATE means
	// a successful Chatwoot send and the DB-side ack can never disagree. Any
	// failure here is returned so asynq retries — the idempotency guard above
	// will short-circuit the next attempt without re-calling Chatwoot.
	if err := w.Queries.SetMessageDelivered(ctx, msg.ID, cwMsg.ID); err != nil {
		log.Error().Err(err).Str("kind", "worker.db.error.set_delivered").Int64("cw_message_id", cwMsg.ID).Msg("set delivered")
		return fmt.Errorf("worker: set delivered: %w", err)
	}

	log.Info().
		Str("kind", "worker.send.succeeded").
		Str("queue", queue.QueueWAtoCW).
		Int64("cw_message_id", cwMsg.ID).
		Msg("delivered")
	return nil
}

// tryUpdateStatus writes a status transition and logs on failure with a structured
// kind. It deliberately does NOT return the error: callers reach this helper for
// non-terminal transitions where the next external step is still safe to attempt
// (the worker's own progress is what matters; a missed updated_at is recoverable
// via janitor). Use the queries directly when the caller needs to bubble the error.
func tryUpdateStatus(ctx context.Context, q *repo.Queries, log *zerolog.Logger, id uuid.UUID, status repo.MsgStatus, errMsg *string, kind string) {
	if err := q.UpdateMessageStatus(ctx, id, status, errMsg); err != nil {
		log.Error().Err(err).Str("kind", "worker.db.error."+kind).Str("status", string(status)).Msg("update status")
	}
}

func classify(err error, kind string) error {
	if isRetriable(err) {
		return fmt.Errorf("[%s retriable]: %w", kind, err)
	}
	return fmt.Errorf("[%s non-retriable]: %w: %w", kind, err, asynq.SkipRetry)
}

func jidToPhone(jid string) string {
	at := strings.Index(jid, "@")
	if at == 0 {
		// JID like "@x" has no number — would otherwise propagate "+@x" downstream.
		return ""
	}
	num := jid
	if at > 0 {
		num = jid[:at]
	}
	if num == "" {
		return ""
	}
	if strings.HasPrefix(num, "+") {
		return num
	}
	return "+" + num
}

func nameOr(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}

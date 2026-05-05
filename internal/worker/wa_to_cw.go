package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"

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

	_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusSending, nil)

	var waMsg megaapi.WebhookMessage
	if err := json.Unmarshal(msg.Payload, &waMsg); err != nil {
		errMsg := err.Error()
		_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusFailed, &errMsg)
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

	_ = w.Queries.SetMessageContact(ctx, msg.ID, contact.ID)

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
		_ = w.Queries.SetContactConversation(ctx, contact.ID, convoID)
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

	_ = w.Queries.SetMessageCWID(ctx, msg.ID, cwMsg.ID)
	_ = w.Queries.UpdateMessageStatus(ctx, msg.ID, repo.StatusDelivered, nil)

	log.Info().
		Str("kind", "worker.send.succeeded").
		Str("queue", queue.QueueWAtoCW).
		Int64("cw_message_id", cwMsg.ID).
		Msg("delivered")
	return nil
}

func classify(err error, kind string) error {
	if isRetriable(err) {
		return fmt.Errorf("[%s retriable]: %w", kind, err)
	}
	return fmt.Errorf("[%s non-retriable]: %w: %w", kind, err, asynq.SkipRetry)
}

func jidToPhone(jid string) string {
	at := strings.Index(jid, "@")
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

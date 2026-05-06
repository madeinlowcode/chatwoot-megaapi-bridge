package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Job struct {
	TenantID  uuid.UUID
	MessageID uuid.UUID
	Payload   []byte
}

var retryBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

func (s *Server) RunWorkers(ctx context.Context) {
	n := s.Cfg.Workers
	if n <= 0 {
		n = runtime.GOMAXPROCS(0) * 4
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go s.runPool(ctx, &wg, s.Inbox, s.processInbound)
		go s.runPool(ctx, &wg, s.Outbox, s.processOutbound)
	}
	wg.Wait()
}

func (s *Server) runPool(ctx context.Context, wg *sync.WaitGroup, ch <-chan Job,
	fn func(context.Context, Job) error) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-ch:
			if !ok {
				return
			}
			s.runJob(ctx, job, fn)
		}
	}
}

func runRetryLoop(ctx context.Context, backoffs []time.Duration, attempt func() error) error {
	var err error
	for i := 0; i <= len(backoffs); i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[i-1]):
			}
		}
		if err = attempt(); err == nil {
			return nil
		}
		if !isRetriable(err) {
			return err
		}
	}
	return err
}

func (s *Server) runJob(ctx context.Context, job Job,
	fn func(context.Context, Job) error) {
	err := runRetryLoop(ctx, retryBackoff, func() error {
		_ = s.DB.IncrementAttempts(ctx, job.MessageID)
		return fn(ctx, job)
	})
	if err == nil {
		if e := s.DB.MarkStatus(ctx, job.MessageID, "done", ""); e != nil {
			s.Log.Err(e).Str("msg_id", job.MessageID.String()).
				Msg("mark done failed — message may be replayed on restart")
		}
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	s.Log.Err(err).Str("msg_id", job.MessageID.String()).Msg("job failed")
	if e := s.DB.MarkStatus(ctx, job.MessageID, "failed", err.Error()); e != nil {
		s.Log.Err(e).Str("msg_id", job.MessageID.String()).Msg("mark failed update failed")
	}
}

func (s *Server) RecoverPending(ctx context.Context) error {
	msgs, err := s.DB.NextPending(ctx, s.Cfg.BufferLimit*2)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		ch := s.Inbox
		if m.Direction == directionOut {
			ch = s.Outbox
		}
		job := Job{TenantID: m.TenantID, MessageID: m.ID, Payload: m.Payload}
		select {
		case ch <- job:
		default:
			s.Log.Warn().Str("msg_id", m.ID.String()).Msg("recovery: queue full, skipping")
		}
	}
	return nil
}

type waPayload struct {
	Key struct {
		ID        string `json:"id"`
		RemoteJid string `json:"remoteJid"`
		FromMe    bool   `json:"fromMe"`
	} `json:"key"`
	PushName string `json:"pushName"`
	Message  struct {
		Conversation string `json:"conversation"`
		Extended     struct {
			Text string `json:"text"`
		} `json:"extendedTextMessage"`
		Image struct {
			URL      string `json:"url"`
			MimeType string `json:"mimetype"`
			Caption  string `json:"caption"`
		} `json:"imageMessage"`
		Audio struct {
			URL      string `json:"url"`
			MimeType string `json:"mimetype"`
			PTT      bool   `json:"ptt"`
		} `json:"audioMessage"`
		Sticker struct {
			URL      string `json:"url"`
			MimeType string `json:"mimetype"`
		} `json:"stickerMessage"`
		Video struct {
			URL      string `json:"url"`
			MimeType string `json:"mimetype"`
			Caption  string `json:"caption"`
		} `json:"videoMessage"`
		Document struct {
			URL      string `json:"url"`
			MimeType string `json:"mimetype"`
			FileName string `json:"fileName"`
			Caption  string `json:"caption"`
		} `json:"documentMessage"`
	} `json:"message"`
}

type Attachment struct {
	URL      string
	MimeType string
	Caption  string
	FileName string
	Kind     string // "image" | "audio" | "video" | "document" | "sticker"
}

type cwPayload struct {
	Event        string `json:"event"`
	MessageType  string `json:"message_type"`
	Private      bool   `json:"private"`
	Content      string `json:"content"`
	ID           int64  `json:"id"`
	Conversation struct {
		ID           int64 `json:"id"`
		ContactInbox struct {
			SourceID string `json:"source_id"`
		} `json:"contact_inbox"`
	} `json:"conversation"`
	Sender struct {
		Name        string `json:"name"`
		PhoneNumber string `json:"phone_number"`
	} `json:"sender"`
	Attachments []struct {
		FileType string `json:"file_type"`
		DataURL  string `json:"data_url"`
	} `json:"attachments"`
}

func extractWAExternalID(body []byte) (string, bool) {
	var p waPayload
	if err := json.Unmarshal(body, &p); err != nil || p.Key.ID == "" {
		return "", false
	}
	return p.Key.ID, true
}

func extractCWExternalID(body []byte) (string, bool) {
	var p cwPayload
	if err := json.Unmarshal(body, &p); err != nil || p.ID == 0 {
		return "", false
	}
	return fmt.Sprintf("cw-%d", p.ID), true
}

func chatwootShouldRelay(body []byte) bool {
	var p cwPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return false
	}
	return p.Event == "message_created" && p.MessageType == "outgoing" && !p.Private
}

func parseWA(body []byte) (waPayload, error) {
	var p waPayload
	err := json.Unmarshal(body, &p)
	return p, err
}

func parseCW(body []byte) (cwPayload, error) {
	var p cwPayload
	err := json.Unmarshal(body, &p)
	return p, err
}

func cwAttachments(p cwPayload) []Attachment {
	if len(p.Attachments) == 0 {
		return nil
	}
	out := make([]Attachment, 0, len(p.Attachments))
	for _, a := range p.Attachments {
		if a.DataURL == "" {
			continue
		}
		out = append(out, Attachment{URL: a.DataURL, Kind: cwTypeToMega(a.FileType)})
	}
	return out
}

func cwTypeToMega(ft string) string {
	switch ft {
	case "image":
		return "image"
	case "audio":
		return "audio"
	case "video":
		return "video"
	default:
		return "document"
	}
}

func waText(p waPayload) string {
	if p.Message.Conversation != "" {
		return p.Message.Conversation
	}
	return p.Message.Extended.Text
}

func waAttachment(p waPayload) (Attachment, bool) {
	if p.Message.Image.URL != "" {
		return Attachment{
			URL: p.Message.Image.URL, MimeType: p.Message.Image.MimeType,
			Caption: p.Message.Image.Caption, Kind: "image",
		}, true
	}
	if p.Message.Audio.URL != "" {
		return Attachment{
			URL: p.Message.Audio.URL, MimeType: p.Message.Audio.MimeType, Kind: "audio",
		}, true
	}
	if p.Message.Sticker.URL != "" {
		return Attachment{
			URL: p.Message.Sticker.URL, MimeType: p.Message.Sticker.MimeType, Kind: "image",
		}, true
	}
	if p.Message.Video.URL != "" {
		return Attachment{
			URL: p.Message.Video.URL, MimeType: p.Message.Video.MimeType,
			Caption: p.Message.Video.Caption, Kind: "video",
		}, true
	}
	if p.Message.Document.URL != "" {
		return Attachment{
			URL: p.Message.Document.URL, MimeType: p.Message.Document.MimeType,
			Caption: p.Message.Document.Caption, FileName: p.Message.Document.FileName, Kind: "document",
		}, true
	}
	return Attachment{}, false
}

func waContactJID(p waPayload) string {
	jid := p.Key.RemoteJid
	if i := strings.Index(jid, "@"); i >= 0 {
		return jid[:i]
	}
	return jid
}

func (s *Server) processInbound(ctx context.Context, job Job) error {
	tenant, err := s.tenantByID(ctx, job.TenantID)
	if err != nil {
		return err
	}
	p, err := parseWA(job.Payload)
	if err != nil {
		return notRetriable(err)
	}
	jid := waContactJID(p)
	contactID, convID, err := s.resolveContact(ctx, tenant, jid, p.PushName)
	if err != nil {
		return err
	}
	if err := s.DB.UpsertContact(ctx, Contact{
		TenantID: tenant.ID, WAJid: jid,
		CWContactID: contactID, CWConversationID: convID,
	}); err != nil {
		return err
	}
	var atts []Attachment
	if a, ok := waAttachment(p); ok {
		atts = []Attachment{a}
	}
	content := waText(p)
	if content == "" && len(atts) > 0 {
		content = atts[0].Caption
	}
	return s.postChatwootMessage(ctx, tenant, convID, content, p.Key.ID, atts)
}

func (s *Server) processOutbound(ctx context.Context, job Job) error {
	tenant, err := s.tenantByID(ctx, job.TenantID)
	if err != nil {
		return err
	}
	p, err := parseCW(job.Payload)
	if err != nil {
		return notRetriable(err)
	}
	jid := p.Conversation.ContactInbox.SourceID
	if jid == "" {
		jid = p.Sender.PhoneNumber
	}
	if jid == "" || p.Content == "" {
		return notRetriable(errors.New("missing recipient or content"))
	}
	return s.sendMegaAPIText(ctx, tenant, jid, p.Content)
}

func (s *Server) tenantByID(ctx context.Context, id uuid.UUID) (Tenant, error) {
	const q = `SELECT id, slug, megaapi_host, megaapi_instance, megaapi_token_enc,
chatwoot_url, chatwoot_token_enc, chatwoot_account_id, chatwoot_inbox_id,
hmac_secret_enc, webhook_bearer_enc FROM tenants WHERE id = $1`
	var t Tenant
	err := s.DB.Pool.QueryRow(ctx, q, id).Scan(&t.ID, &t.Slug, &t.MegaAPIHost,
		&t.MegaAPIInstance, &t.MegaAPITokenEnc, &t.ChatwootURL, &t.ChatwootTokenEnc,
		&t.ChatwootAccountID, &t.ChatwootInboxID, &t.HMACSecretEnc, &t.WebhookBearerEnc)
	return t, err
}

type retriableError struct{ err error }

func (e retriableError) Error() string { return e.err.Error() }
func (e retriableError) Unwrap() error { return e.err }

type fatalError struct{ err error }

func (e fatalError) Error() string { return e.err.Error() }
func (e fatalError) Unwrap() error { return e.err }

func notRetriable(err error) error { return fatalError{err} }
func retriable(err error) error    { return retriableError{err} }

func isRetriable(err error) bool {
	var fe fatalError
	return !errors.As(err, &fe)
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) resolveContact(ctx context.Context, t Tenant, jid, name string) (int64, int64, error) {
	c, err := s.DB.GetContact(ctx, t.ID, jid)
	if err == nil {
		return c.CWContactID, c.CWConversationID, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return 0, 0, retriable(err)
	}
	contactID, err := s.cwCreateContact(ctx, t, jid, name)
	if err != nil {
		return 0, 0, err
	}
	convID, err := s.cwCreateConversation(ctx, t, contactID, jid)
	if err != nil {
		return 0, 0, err
	}
	return contactID, convID, nil
}

func (s *Server) cwCreateContact(ctx context.Context, t Tenant, jid, name string) (int64, error) {
	body := map[string]any{
		"inbox_id":     t.ChatwootInboxID,
		"name":         displayName(name, jid),
		"phone_number": "+" + jid,
		"identifier":   jid,
	}
	url := fmt.Sprintf("%s/api/v1/accounts/%d/contacts", strings.TrimRight(t.ChatwootURL, "/"), t.ChatwootAccountID)
	var resp struct {
		Payload struct {
			Contact struct {
				ID int64 `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	if err := s.cwDo(ctx, t, http.MethodPost, url, body, &resp); err != nil {
		return 0, err
	}
	return resp.Payload.Contact.ID, nil
}

func (s *Server) cwCreateConversation(ctx context.Context, t Tenant, contactID int64, jid string) (int64, error) {
	body := map[string]any{
		"inbox_id":   t.ChatwootInboxID,
		"contact_id": contactID,
		"source_id":  jid,
	}
	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations", strings.TrimRight(t.ChatwootURL, "/"), t.ChatwootAccountID)
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := s.cwDo(ctx, t, http.MethodPost, url, body, &resp); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

func (s *Server) postChatwootMessage(ctx context.Context, t Tenant, convID int64, content, externalID string, attachments []Attachment) error {
	body := map[string]any{
		"content":            content,
		"message_type":       "incoming",
		"content_attributes": map[string]any{"external_id": externalID},
	}
	if len(attachments) > 0 {
		out := make([]map[string]any, 0, len(attachments))
		for _, a := range attachments {
			out = append(out, map[string]any{"file_url": a.URL, "file_type": a.Kind})
		}
		body["attachments"] = out
	}
	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations/%d/messages",
		strings.TrimRight(t.ChatwootURL, "/"), t.ChatwootAccountID, convID)
	return s.cwDo(ctx, t, http.MethodPost, url, body, nil)
}

func (s *Server) cwDo(ctx context.Context, t Tenant, method, url string, in any, out any) error {
	tok, err := Decrypt(t.ChatwootTokenEnc, s.Key)
	if err != nil {
		return notRetriable(err)
	}
	resp, err := s.cwSend(ctx, method, url, string(tok), in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := classifyHTTP(resp, "chatwoot "+url); err != nil {
		return err
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (s *Server) cwSend(ctx context.Context, method, url, tok string, in any) (*http.Response, error) {
	buf, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return nil, notRetriable(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", tok)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, retriable(err)
	}
	return resp, nil
}

func classifyHTTP(resp *http.Response, label string) error {
	if resp.StatusCode >= 500 {
		return retriable(fmt.Errorf("%s %d", label, resp.StatusCode))
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return notRetriable(fmt.Errorf("%s %d: %s", label, resp.StatusCode, body))
	}
	return nil
}

func (s *Server) sendMegaAPIText(ctx context.Context, t Tenant, to, text string) error {
	tok, err := Decrypt(t.MegaAPITokenEnc, s.Key)
	if err != nil {
		return notRetriable(err)
	}
	body := map[string]any{"messageData": map[string]any{
		"to": to, "text": text, "isGroup": false, "linkPreview": false,
	}}
	url := fmt.Sprintf("%s/rest/sendMessage/%s/text",
		strings.TrimRight(t.MegaAPIHost, "/"), t.MegaAPIInstance)
	resp, err := bearerPost(ctx, url, string(tok), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return classifyHTTP(resp, "megaapi")
}

func bearerPost(ctx context.Context, url, tok string, in any) (*http.Response, error) {
	buf, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, notRetriable(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, retriable(err)
	}
	return resp, nil
}

func displayName(name, jid string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return jid
}

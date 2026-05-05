// Package chatwoot is an HTTP client for Chatwoot's REST API (v1).
package chatwoot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/httpx"
)

type Client interface {
	SearchContact(ctx context.Context, cfg Config, query string) ([]Contact, error)
	CreateContact(ctx context.Context, cfg Config, req CreateContactRequest) (*Contact, error)
	CreateConversation(ctx context.Context, cfg Config, req CreateConversationRequest) (*Conversation, error)
	CreateMessage(ctx context.Context, cfg Config, conversationID int64, req CreateMessageRequest) (*Message, error)
}

type HTTPClient struct {
	httpC *http.Client
}

func New() *HTTPClient {
	return &HTTPClient{httpC: httpx.SharedClient()}
}

func NewWithClient(c *http.Client) *HTTPClient {
	return &HTTPClient{httpC: c}
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Status int
	Body   string
	URL    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("chatwoot: %s -> HTTP %d: %s", e.URL, e.Status, truncate(e.Body, 256))
}

func (e *APIError) Retriable() bool {
	return e.Status >= 500 || e.Status == 408 || e.Status == 429
}

func (c *HTTPClient) SearchContact(ctx context.Context, cfg Config, query string) ([]Contact, error) {
	u, err := buildURL(cfg.BaseURL, fmt.Sprintf("/api/v1/accounts/%d/contacts/search", cfg.AccountID))
	if err != nil {
		return nil, err
	}
	u += "?q=" + url.QueryEscape(query)
	raw, err := c.do(ctx, http.MethodGet, u, cfg.APIToken, nil)
	if err != nil {
		return nil, err
	}
	var sr SearchContactsResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, fmt.Errorf("chatwoot: parse search: %w", err)
	}
	return sr.Payload, nil
}

func (c *HTTPClient) CreateContact(ctx context.Context, cfg Config, req CreateContactRequest) (*Contact, error) {
	if req.InboxID == 0 {
		req.InboxID = cfg.InboxID
	}
	u, err := buildURL(cfg.BaseURL, fmt.Sprintf("/api/v1/accounts/%d/contacts", cfg.AccountID))
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, u, cfg.APIToken, req)
	if err != nil {
		return nil, err
	}
	var cr CreateContactResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("chatwoot: parse create contact: %w", err)
	}
	if cr.Payload.Contact.ID == 0 {
		// some chatwoot versions return the contact at top level
		var c2 Contact
		if err := json.Unmarshal(raw, &c2); err == nil && c2.ID != 0 {
			return &c2, nil
		}
		return nil, errors.New("chatwoot: create contact returned no id")
	}
	return &cr.Payload.Contact, nil
}

func (c *HTTPClient) CreateConversation(ctx context.Context, cfg Config, req CreateConversationRequest) (*Conversation, error) {
	if req.InboxID == 0 {
		req.InboxID = cfg.InboxID
	}
	if req.Status == "" {
		req.Status = "open"
	}
	u, err := buildURL(cfg.BaseURL, fmt.Sprintf("/api/v1/accounts/%d/conversations", cfg.AccountID))
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, u, cfg.APIToken, req)
	if err != nil {
		return nil, err
	}
	var conv Conversation
	if err := json.Unmarshal(raw, &conv); err != nil {
		return nil, fmt.Errorf("chatwoot: parse conversation: %w", err)
	}
	if conv.ID == 0 {
		return nil, errors.New("chatwoot: create conversation returned no id")
	}
	return &conv, nil
}

func (c *HTTPClient) CreateMessage(ctx context.Context, cfg Config, conversationID int64, req CreateMessageRequest) (*Message, error) {
	if req.MessageType == "" {
		req.MessageType = "incoming"
	}
	u, err := buildURL(cfg.BaseURL,
		fmt.Sprintf("/api/v1/accounts/%d/conversations/%s/messages",
			cfg.AccountID, strconv.FormatInt(conversationID, 10)))
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, u, cfg.APIToken, req)
	if err != nil {
		return nil, err
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("chatwoot: parse message: %w", err)
	}
	if m.ID == 0 {
		return nil, errors.New("chatwoot: create message returned no id")
	}
	return &m, nil
}

func (c *HTTPClient) do(ctx context.Context, method, u, token string, body any) ([]byte, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("chatwoot: marshal: %w", err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, buf)
	if err != nil {
		return nil, fmt.Errorf("chatwoot: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("api_access_token", token)
	req.Header.Set("Accept", "application/json")

	res, err := c.httpC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatwoot: do: %w", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("chatwoot: read: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, &APIError{Status: res.StatusCode, Body: string(out), URL: u}
	}
	return out, nil
}

func buildURL(base, path string) (string, error) {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "", errors.New("chatwoot: empty base url")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return "", errors.New("chatwoot: base url must include scheme")
	}
	return base + path, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Package megaapi is an HTTP client for the megaAPI WhatsApp bridge service.
package megaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/httpx"
)

// Client is the contract every consumer depends on.
type Client interface {
	SendText(ctx context.Context, host, instanceKey, token, to, text string) error
	ConfigWebhook(ctx context.Context, host, instanceKey, token, webhookURL string) error
	InstanceStatus(ctx context.Context, host, instanceKey, token string) (*Status, error)
}

// HTTPClient is a real implementation backed by net/http.
type HTTPClient struct {
	httpC *http.Client
}

// New returns an HTTPClient using the shared transport.
func New() *HTTPClient {
	return &HTTPClient{httpC: httpx.SharedClient()}
}

// NewWithClient lets tests inject their own *http.Client.
func NewWithClient(c *http.Client) *HTTPClient {
	return &HTTPClient{httpC: c}
}

// APIError is returned for non-2xx responses; consumers can classify retriable vs not.
type APIError struct {
	Status int
	Body   string
	URL    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("megaapi: %s -> HTTP %d: %s", e.URL, e.Status, truncate(e.Body, 256))
}

// Retriable reports whether this error should trigger an asynq retry.
func (e *APIError) Retriable() bool {
	return e.Status >= 500 || e.Status == 408 || e.Status == 429
}

// SendText posts a text message via {host}/rest/sendMessage/{instance_key}/text.
func (c *HTTPClient) SendText(ctx context.Context, host, instanceKey, token, to, text string) error {
	u, err := joinPath(host, "rest", "sendMessage", instanceKey, "text")
	if err != nil {
		return err
	}
	body := SendTextRequest{MessageData: SendTextData{To: to, Text: text}}
	_, err = c.doJSON(ctx, http.MethodPost, u, token, body)
	return err
}

// ConfigWebhook configures the webhook URL on the megaAPI instance.
func (c *HTTPClient) ConfigWebhook(ctx context.Context, host, instanceKey, token, webhookURL string) error {
	u, err := joinPath(host, "rest", "webhook", instanceKey, "configWebhook")
	if err != nil {
		return err
	}
	body := ConfigWebhookRequest{MessageData: ConfigWebhookData{
		WebhookURL: webhookURL, WebhookEnabled: true,
	}}
	_, err = c.doJSON(ctx, http.MethodPost, u, token, body)
	return err
}

// InstanceStatus calls GET {host}/rest/instance/{instance_key}/me.
func (c *HTTPClient) InstanceStatus(ctx context.Context, host, instanceKey, token string) (*Status, error) {
	u, err := joinPath(host, "rest", "instance", instanceKey, "me")
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(ctx, http.MethodGet, u, token, nil)
	if err != nil {
		return nil, err
	}
	st := &Status{Raw: raw}
	_ = json.Unmarshal(raw, st)
	return st, nil
}

func (c *HTTPClient) doJSON(ctx context.Context, method, u, token string, body any) ([]byte, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("megaapi: marshal: %w", err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, buf)
	if err != nil {
		return nil, fmt.Errorf("megaapi: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	res, err := c.httpC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("megaapi: do: %w", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("megaapi: read: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, &APIError{Status: res.StatusCode, Body: string(out), URL: u}
	}
	return out, nil
}

// joinPath builds {host}/{segments...} validating that host has https:// scheme.
func joinPath(host string, segments ...string) (string, error) {
	host = strings.TrimRight(host, "/")
	if !strings.HasPrefix(host, "https://") && !strings.HasPrefix(host, "http://") {
		return "", errors.New("megaapi: host must include scheme")
	}
	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("megaapi: parse host: %w", err)
	}
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" {
			return "", errors.New("megaapi: empty path segment")
		}
		parts = append(parts, url.PathEscape(s))
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.Join(parts, "/")
	return u.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

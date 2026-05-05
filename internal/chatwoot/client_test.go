package chatwoot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mkConfig(srv *httptest.Server) Config {
	return Config{
		BaseURL:   srv.URL,
		APIToken:  "TOKEN",
		AccountID: 1,
		InboxID:   42,
	}
}

func TestSearchContactBuildsURL(t *testing.T) {
	var path, qs, tok string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		qs = r.URL.RawQuery
		tok = r.Header.Get("api_access_token")
		_, _ = w.Write([]byte(`{"payload":[{"id":7,"name":"x"}],"meta":{"count":1}}`))
	}))
	defer srv.Close()
	cs, err := NewWithClient(srv.Client()).SearchContact(context.Background(), mkConfig(srv), "+5511")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/accounts/1/contacts/search" {
		t.Errorf("path = %q", path)
	}
	if qs != "q=%2B5511" {
		t.Errorf("qs = %q", qs)
	}
	if tok != "TOKEN" {
		t.Errorf("token header = %q", tok)
	}
	if len(cs) != 1 || cs[0].ID != 7 {
		t.Errorf("contacts = %+v", cs)
	}
}

func TestCreateContact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CreateContactRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.InboxID != 42 {
			t.Errorf("inbox_id = %d", req.InboxID)
		}
		_, _ = w.Write([]byte(`{"payload":{"contact":{"id":99,"name":"x"}}}`))
	}))
	defer srv.Close()
	c, err := NewWithClient(srv.Client()).CreateContact(context.Background(), mkConfig(srv),
		CreateContactRequest{Name: "x", PhoneNumber: "+5511", Identifier: "wa-jid"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != 99 {
		t.Errorf("id = %d", c.ID)
	}
}

func TestCreateMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CreateMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.MessageType != "incoming" || req.Content != "hi" {
			t.Errorf("req = %+v", req)
		}
		_, _ = w.Write([]byte(`{"id":555,"content":"hi","conversation_id":1}`))
	}))
	defer srv.Close()
	m, err := NewWithClient(srv.Client()).CreateMessage(context.Background(), mkConfig(srv), 1,
		CreateMessageRequest{Content: "hi", MessageType: "incoming"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != 555 {
		t.Errorf("id = %d", m.ID)
	}
}

func TestCreateConversation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":7,"status":"open","inbox_id":42}`))
	}))
	defer srv.Close()
	conv, err := NewWithClient(srv.Client()).CreateConversation(context.Background(), mkConfig(srv),
		CreateConversationRequest{SourceID: "wa-jid", ContactID: 99})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID != 7 {
		t.Errorf("conv id = %d", conv.ID)
	}
}

func TestErrorClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	_, err := NewWithClient(srv.Client()).CreateMessage(context.Background(), mkConfig(srv), 1,
		CreateMessageRequest{Content: "x"})
	apiErr, ok := err.(*APIError)
	if !ok || !apiErr.Retriable() {
		t.Errorf("expected retriable APIError, got %v", err)
	}
}

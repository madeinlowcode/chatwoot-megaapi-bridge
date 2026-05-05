package megaapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTextSuccess(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody SendTextRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewWithClient(srv.Client())
	if err := c.SendText(context.Background(), srv.URL, "INST", "TOKEN", "55119@s.whatsapp.net", "hi"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if want := "/rest/sendMessage/INST/text"; gotPath != want {
		t.Errorf("path = %q want %q", gotPath, want)
	}
	if gotAuth != "Bearer TOKEN" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.MessageData.To != "55119@s.whatsapp.net" || gotBody.MessageData.Text != "hi" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestSendText5xxIsRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()
	c := NewWithClient(srv.Client())
	err := c.SendText(context.Background(), srv.URL, "x", "t", "to", "txt")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !apiErr.Retriable() {
		t.Errorf("503 must be retriable")
	}
}

func TestSendText4xxNotRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	err := NewWithClient(srv.Client()).SendText(context.Background(), srv.URL, "x", "t", "to", "txt")
	apiErr, _ := err.(*APIError)
	if apiErr == nil || apiErr.Retriable() {
		t.Errorf("4xx must NOT be retriable, got %v", err)
	}
}

func TestConfigWebhook(t *testing.T) {
	var path string
	var body ConfigWebhookRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()
	if err := NewWithClient(srv.Client()).ConfigWebhook(context.Background(), srv.URL, "K", "T", "https://x/v1/wa/demo"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/rest/webhook/K/configWebhook") {
		t.Errorf("path = %q", path)
	}
	if body.MessageData.WebhookURL != "https://x/v1/wa/demo" || !body.MessageData.WebhookEnabled {
		t.Errorf("body = %+v", body)
	}
}

func TestInstanceStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"connected":true,"me":"55119@s.whatsapp.net"}`))
	}))
	defer srv.Close()
	st, err := NewWithClient(srv.Client()).InstanceStatus(context.Background(), srv.URL, "K", "T")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected {
		t.Errorf("connected = false")
	}
}

func TestJoinPathRejectsNoScheme(t *testing.T) {
	if _, err := joinPath("foo.example", "x"); err == nil {
		t.Fatal("expected error for missing scheme")
	}
}

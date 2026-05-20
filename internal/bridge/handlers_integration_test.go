//go:build integration

package bridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// makeAuthedTenant inserts a tenant whose webhook bearer + hmac secret are
// encrypted with `key` so the route handler can decrypt them at runtime.
func makeAuthedTenant(t *testing.T, db *DB, key []byte, slug, bearer, hmacSecret string) Tenant {
	t.Helper()
	encMega, err := Encrypt([]byte("mega-tok"), key)
	require.NoError(t, err)
	encCW, err := Encrypt([]byte("cw-tok"), key)
	require.NoError(t, err)
	encBearer, err := Encrypt([]byte(bearer), key)
	require.NoError(t, err)
	encHMAC, err := Encrypt([]byte(hmacSecret), key)
	require.NoError(t, err)
	id, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug:              slug,
		MegaAPIHost:       "https://x",
		MegaAPIInstance:   "i",
		MegaAPITokenEnc:   encMega,
		ChatwootURL:       "https://c",
		ChatwootTokenEnc:  encCW,
		ChatwootAccountID: 1,
		ChatwootInboxID:   2,
		HMACSecretEnc:     encHMAC,
		WebhookBearerEnc:  encBearer,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, id)
	})
	got, err := db.GetTenantBySlug(context.Background(), slug)
	require.NoError(t, err)
	return got
}

func newServerWithDB(db *DB, key []byte, buf int) *Server {
	cfg := Config{BufferLimit: buf}
	return NewServer(db, key, cfg, zerolog.Nop())
}

func TestHandleWAWebhook_RejectsMissingBearer(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-wa-1", "right", "h")
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"key":{"id":"ABC","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-wa-1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleWAWebhook_RejectsWrongBearer(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-wa-2", "right", "h")
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"key":{"id":"ABC","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-wa-2", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleWAWebhook_AcceptsRightBearerAndQueues(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-wa-3", "right", "h")
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"key":{"id":"ABC","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-wa-3", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "queued")
	require.Equal(t, 1, len(s.Inbox))
}

func TestHandleCWWebhook_RejectsTamperedHMAC(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	secret := "cw-secret"
	makeAuthedTenant(t, db, key, "demo-cw-1", "b", secret)
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"id":1,"event":"message_created","message_type":"outgoing","content":"hi"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("different-body"))
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo-cw-1", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleCWWebhook_AcceptsValidHMACAndQueues(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	secret := "cw-secret-2"
	makeAuthedTenant(t, db, key, "demo-cw-2", "b", secret)
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"id":7,"event":"message_created","message_type":"outgoing","content":"hi","conversation":{"id":99,"contact_inbox":{"source_id":"5511"}}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo-cw-2", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "queued")
	require.Equal(t, 1, len(s.Outbox))
}

func TestEnqueue_DuplicateExternalIDReturnsDuplicate(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-dup", "right", "h")
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"key":{"id":"DUP1","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-dup", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer right")
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		return rec
	}
	first := post()
	require.Equal(t, http.StatusOK, first.Code)
	require.Contains(t, first.Body.String(), "queued")

	second := post()
	require.Equal(t, http.StatusOK, second.Code)
	require.Contains(t, second.Body.String(), "duplicate")
}

func TestEnqueue_QueueFullReturns503AndMarksFailed(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	tn := makeAuthedTenant(t, db, key, "demo-full", "right", "h")
	s := newServerWithDB(db, key, 1)
	// Fill the channel
	s.Inbox <- Job{TenantID: tn.ID, MessageID: uuid.New()}

	body := []byte(`{"key":{"id":"FULL1","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-full", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "full")

	// Verify the message row was marked 'failed' with last_error='queue full'.
	var status, lastErr string
	err := db.Pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(last_error,'') FROM messages WHERE tenant_id=$1 AND external_id=$2`,
		tn.ID, "FULL1").Scan(&status, &lastErr)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
	require.Equal(t, "queue full", lastErr)
}

func TestReadyz_DBDownReturns503(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	s := newServerWithDB(db, key, 4)
	db.Close()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "down")
}

func TestReadyz_QueueFullReturns503(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	s := newServerWithDB(db, key, 1)
	s.Inbox <- Job{}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "full")
}

func TestRecoverPending_RoutesByDirection(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	tn := makeAuthedTenant(t, db, key, "demo-rec-1", "b", "h")

	_, _, err := db.InsertMessage(context.Background(), Message{
		TenantID: tn.ID, Direction: directionIn, ExternalID: "in-1", Payload: []byte(`{}`),
	})
	require.NoError(t, err)
	_, _, err = db.InsertMessage(context.Background(), Message{
		TenantID: tn.ID, Direction: directionOut, ExternalID: "out-1", Payload: []byte(`{}`),
	})
	require.NoError(t, err)

	s := newServerWithDB(db, key, 4)
	require.NoError(t, s.RecoverPending(context.Background()))
	require.Equal(t, 1, len(s.Inbox))
	require.Equal(t, 1, len(s.Outbox))
}

func TestProcessOutbound_MultipleAttachments_CaptionOnlyOnFirst(t *testing.T) {
	db := setupDB(t)
	captions := []string{}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		md, _ := body["messageData"].(map[string]any)
		captions = append(captions, fmt.Sprint(md["caption"]))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer mock.Close()
	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-out", MegaAPIHost: mock.URL, MegaAPIInstance: "abc",
		MegaAPITokenEnc: tokEnc, ChatwootURL: "http://x", ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})
	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"id":1,
		"content":"hello",
		"conversation":{"id":1,"contact_inbox":{"source_id":"5511999999999"}},
		"attachments":[
			{"file_type":"image","data_url":"https://m/1.jpg"},
			{"file_type":"image","data_url":"https://m/2.jpg"},
			{"file_type":"image","data_url":"https://m/3.jpg"}
		]
	}`)
	require.NoError(t, s.processOutbound(context.Background(), Job{TenantID: tID, Payload: body}))
	require.Equal(t, 3, len(captions))
	require.Equal(t, "hello", captions[0])
	require.Equal(t, "", captions[1])
	require.Equal(t, "", captions[2])
}

func TestProcessInbound_ImageMessage_PostsAttachmentToCW(t *testing.T) {
	db := setupDB(t)
	var capturedBody map[string]any
	cwMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/messages"):
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/contacts"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"payload":{"contact":{"id":11}}}`))
		case strings.Contains(r.URL.Path, "/conversations"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":99}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer cwMock.Close()
	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-in", MegaAPIHost: "http://x", MegaAPIInstance: "abc",
		MegaAPITokenEnc: tokEnc, ChatwootURL: cwMock.URL, ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})
	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"key":{"id":"WAID-IMG","remoteJid":"5511999999999@s.whatsapp.net","fromMe":false},
		"pushName":"Alice",
		"message":{"imageMessage":{"url":"https://media.example/img.jpg","mimetype":"image/jpeg","caption":"hello"}}
	}`)
	require.NoError(t, s.processInbound(context.Background(), Job{TenantID: tID, Payload: body}))
	atts, _ := capturedBody["attachments"].([]any)
	require.Equal(t, 1, len(atts), "expected 1 attachment")
	first := atts[0].(map[string]any)
	require.Equal(t, "https://media.example/img.jpg", first["file_url"])
	require.Equal(t, "image", first["file_type"])
	require.Equal(t, "hello", capturedBody["content"])
}

func TestProcessInbound_DocumentMessage_PostsFileNameAndCaption(t *testing.T) {
	db := setupDB(t)
	var capturedBody map[string]any
	cwMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/messages"):
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/contacts"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"payload":{"contact":{"id":12}}}`))
		case strings.Contains(r.URL.Path, "/conversations"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":100}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer cwMock.Close()
	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-doc", MegaAPIHost: "http://x", MegaAPIInstance: "abc",
		MegaAPITokenEnc: tokEnc, ChatwootURL: cwMock.URL, ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})
	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"key":{"id":"WAID-DOC","remoteJid":"5511999999999@s.whatsapp.net","fromMe":false},
		"pushName":"Alice",
		"message":{"documentMessage":{"url":"https://media.example/c.pdf","mimetype":"application/pdf","fileName":"contract.pdf","caption":"sign please"}}
	}`)
	require.NoError(t, s.processInbound(context.Background(), Job{TenantID: tID, Payload: body}))
	require.Equal(t, "sign please", capturedBody["content"])
	atts, _ := capturedBody["attachments"].([]any)
	require.Equal(t, 1, len(atts))
	first := atts[0].(map[string]any)
	require.Equal(t, "document", first["file_type"])
}

func TestHandleWAWebhook_UnknownSlugReturns404(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"key":{"id":"X","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/does-not-exist", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "tenant not found")
}

func TestHandleCWWebhook_UnknownSlugReturns404(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"id":1,"event":"message_created","message_type":"outgoing","content":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/missing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "tenant not found")
}

// DB closed before request simulates an arbitrary lookup failure surfacing as 500.
func TestHandleWAWebhook_DBDownReturns500OnLookup(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-dbdown", "right", "h")
	s := newServerWithDB(db, key, 4)
	db.Close()

	body := []byte(`{"key":{"id":"X","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-dbdown", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "db error")
}

func TestHandleWAWebhook_MissingExternalIDReturns400(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-missing-id", "right", "h")
	s := newServerWithDB(db, key, 4)

	// Empty key.id ⇒ extractWAExternalID returns false ⇒ 400.
	body := []byte(`{"key":{"id":"","remoteJid":"5511@s"},"message":{"conversation":"hi"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-missing-id", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing external id")
}

func TestHandleWAWebhook_MalformedJSONReturns400(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-bad-json", "right", "h")
	s := newServerWithDB(db, key, 4)

	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-bad-json",
		bytes.NewReader([]byte(`{not-json`)))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// readBodyOr400 maps oversize bodies (MaxBytesReader) to 400 — current behaviour.
// Documented here so future change to 413 produces a failing test forcing review.
func TestHandleWAWebhook_OversizeBodyReturns400(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	makeAuthedTenant(t, db, key, "demo-big", "right", "h")
	s := newServerWithDB(db, key, 4)

	big := bytes.Repeat([]byte("a"), maxBodyBytes+10)
	req := httptest.NewRequest(http.MethodPost, "/v1/wa/demo-big", bytes.NewReader(big))
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad body")
}

func TestHandleCWWebhook_MissingExternalIDReturns400(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	secret := "cw-secret"
	makeAuthedTenant(t, db, key, "demo-cw-noid", "b", secret)
	s := newServerWithDB(db, key, 4)

	// id=0 ⇒ extractCWExternalID returns false ⇒ 400.
	body := []byte(`{"id":0,"event":"message_created","message_type":"outgoing","content":"hi"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo-cw-noid", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing external id")
}

func TestHandleCWWebhook_IncomingMessageIsIgnoredWith200(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	secret := "cw-secret-ig"
	makeAuthedTenant(t, db, key, "demo-cw-ignore", "b", secret)
	s := newServerWithDB(db, key, 4)

	// message_type=incoming ⇒ chatwootShouldRelay=false ⇒ 200 ignored, no enqueue.
	body := []byte(`{"id":33,"event":"message_created","message_type":"incoming","content":"hi"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo-cw-ignore", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "ignored")
	require.Equal(t, 0, len(s.Outbox))
}

func TestHandleCWWebhook_PrivateNoteIgnoredWith200(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	secret := "cw-secret-pn"
	makeAuthedTenant(t, db, key, "demo-cw-private", "b", secret)
	s := newServerWithDB(db, key, 4)

	body := []byte(`{"id":34,"event":"message_created","message_type":"outgoing","private":true,"content":"note"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/cw/demo-cw-private", bytes.NewReader(body))
	req.Header.Set("X-Chatwoot-Signature", hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "ignored")
	require.Equal(t, 0, len(s.Outbox))
}

// processInbound must not call cwCreateContact when contact already exists.
// We assert by exposing an httptest server that 500s on /contacts and /conversations
// — if processInbound tried to create, the call would fail; success means it reused.
func TestProcessInbound_ExistingContactSkipsCreate(t *testing.T) {
	db := setupDB(t)
	var contactCalls, convCalls, msgCalls int32
	cwMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/messages"):
			msgCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/contacts"):
			contactCalls++
			w.WriteHeader(http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/conversations"):
			convCalls++
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer cwMock.Close()

	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-reuse", MegaAPIHost: "http://x", MegaAPIInstance: "abc",
		MegaAPITokenEnc: tokEnc, ChatwootURL: cwMock.URL, ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})

	// Seed an existing contact so resolveContact short-circuits.
	require.NoError(t, db.UpsertContact(context.Background(), Contact{
		TenantID: tID, WAJid: "5511999999999", CWContactID: 77, CWConversationID: 88,
	}))

	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"key":{"id":"WAID-REUSE","remoteJid":"5511999999999@s.whatsapp.net","fromMe":false},
		"pushName":"Alice","message":{"conversation":"hi again"}
	}`)
	require.NoError(t, s.processInbound(context.Background(), Job{TenantID: tID, Payload: body}))
	require.Equal(t, int32(0), contactCalls, "must not call /contacts when contact exists")
	require.Equal(t, int32(0), convCalls, "must not call /conversations when contact exists")
	require.Equal(t, int32(1), msgCalls, "must post message exactly once")
}

func TestProcessInbound_NewContactCreatesContactAndConversation(t *testing.T) {
	db := setupDB(t)
	var contactCalls, convCalls, msgCalls int32
	cwMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/messages"):
			msgCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/contacts"):
			contactCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"payload":{"contact":{"id":201}}}`))
		case strings.Contains(r.URL.Path, "/conversations"):
			convCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":555}`))
		}
	}))
	defer cwMock.Close()

	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-newc", MegaAPIHost: "http://x", MegaAPIInstance: "abc",
		MegaAPITokenEnc: tokEnc, ChatwootURL: cwMock.URL, ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})

	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"key":{"id":"WAID-NEW","remoteJid":"5511888888888@s.whatsapp.net","fromMe":false},
		"pushName":"Bob","message":{"conversation":"first ping"}
	}`)
	require.NoError(t, s.processInbound(context.Background(), Job{TenantID: tID, Payload: body}))
	require.Equal(t, int32(1), contactCalls)
	require.Equal(t, int32(1), convCalls)
	require.Equal(t, int32(1), msgCalls)

	// Sanity: UpsertContact stored the returned CW ids.
	c, err := db.GetContact(context.Background(), tID, "5511888888888")
	require.NoError(t, err)
	require.Equal(t, int64(201), c.CWContactID)
	require.Equal(t, int64(555), c.CWConversationID)
}

func TestProcessOutbound_TextOnly_HitsTextEndpoint(t *testing.T) {
	db := setupDB(t)
	var paths []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer mock.Close()

	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-text", MegaAPIHost: mock.URL, MegaAPIInstance: "instX",
		MegaAPITokenEnc: tokEnc, ChatwootURL: "http://x", ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})

	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"id":11,
		"content":"hi text","conversation":{"id":1,"contact_inbox":{"source_id":"5511777"}}
	}`)
	require.NoError(t, s.processOutbound(context.Background(), Job{TenantID: tID, Payload: body}))
	require.Equal(t, 1, len(paths))
	require.Equal(t, "/rest/sendMessage/instX/text", paths[0])
}

func TestProcessOutbound_MissingRecipientAndContent_FatalNotRetriable(t *testing.T) {
	db := setupDB(t)
	key := bytes.Repeat([]byte{1}, 32)
	tokEnc, _ := Encrypt([]byte("tok"), key)
	tID, err := db.InsertTenant(context.Background(), TenantInsert{
		Slug: "demo-empty", MegaAPIHost: "http://x", MegaAPIInstance: "i",
		MegaAPITokenEnc: tokEnc, ChatwootURL: "http://x", ChatwootTokenEnc: tokEnc,
		ChatwootAccountID: 1, ChatwootInboxID: 5,
		HMACSecretEnc: tokEnc, WebhookBearerEnc: tokEnc,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tID)
	})

	s := &Server{Key: key, DB: db}
	body := []byte(`{
		"event":"message_created","message_type":"outgoing","private":false,"id":12,
		"content":"","conversation":{"id":1,"contact_inbox":{"source_id":""}}
	}`)
	err = s.processOutbound(context.Background(), Job{TenantID: tID, Payload: body})
	require.Error(t, err)
	require.False(t, isRetriable(err), "missing recipient must be fatal")
}

// runJob success → message row marked status='done'.
func TestRunJob_SuccessMarksDone(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	tn := makeAuthedTenant(t, db, key, "demo-job-ok", "b", "h")
	s := &Server{Key: key, DB: db, Cfg: Config{BufferLimit: 4}, Log: zerolog.Nop()}

	id, _, err := db.InsertMessage(context.Background(), Message{
		TenantID: tn.ID, Direction: directionIn, ExternalID: "job-ok-1", Payload: []byte(`{}`),
	})
	require.NoError(t, err)

	s.runJob(context.Background(), Job{TenantID: tn.ID, MessageID: id, Payload: []byte(`{}`)},
		func(_ context.Context, _ Job) error { return nil })

	var status string
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT status FROM messages WHERE id=$1`, id).Scan(&status))
	require.Equal(t, "done", status)
}

// runJob fatal failure → status='failed', last_error stores message.
func TestRunJob_FatalErrorMarksFailedWithMessage(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	tn := makeAuthedTenant(t, db, key, "demo-job-fail", "b", "h")
	s := &Server{Key: key, DB: db, Cfg: Config{BufferLimit: 4}, Log: zerolog.Nop()}

	id, _, err := db.InsertMessage(context.Background(), Message{
		TenantID: tn.ID, Direction: directionIn, ExternalID: "job-fail-1", Payload: []byte(`{}`),
	})
	require.NoError(t, err)

	s.runJob(context.Background(), Job{TenantID: tn.ID, MessageID: id, Payload: []byte(`{}`)},
		func(_ context.Context, _ Job) error { return notRetriable(fmt.Errorf("boom-400")) })

	var status, lastErr string
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(last_error,'') FROM messages WHERE id=$1`, id).Scan(&status, &lastErr))
	require.Equal(t, "failed", status)
	require.Contains(t, lastErr, "boom-400")
}

func TestRecoverPending_FullChannelSkipsNotBlocks(t *testing.T) {
	db := setupDB(t)
	key := RandomBytes(32)
	tn := makeAuthedTenant(t, db, key, "demo-rec-2", "b", "h")

	for i := 0; i < 5; i++ {
		_, _, err := db.InsertMessage(context.Background(), Message{
			TenantID:   tn.ID,
			Direction:  directionIn,
			ExternalID: "in-bulk-" + strings.Repeat("x", i+1),
			Payload:    []byte(`{}`),
		})
		require.NoError(t, err)
	}

	s := newServerWithDB(db, key, 1)
	// must not block — even when more pending than channel cap
	require.NoError(t, s.RecoverPending(context.Background()))
}

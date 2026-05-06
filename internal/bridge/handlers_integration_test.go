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

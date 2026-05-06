package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRetriable_DefaultIsRetriable(t *testing.T) {
	require.True(t, isRetriable(errors.New("network")))
}

func TestRetriable_FatalIsNotRetried(t *testing.T) {
	require.False(t, isRetriable(notRetriable(errors.New("400"))))
}

func TestRetriable_RetriableExplicit(t *testing.T) {
	require.True(t, isRetriable(retriable(errors.New("500"))))
}

func TestDisplayName_FallsBackToJID(t *testing.T) {
	require.Equal(t, "5511999", displayName("", "5511999"))
	require.Equal(t, "Alice", displayName("Alice", "5511999"))
}

func TestSendMegaAPIText_4xxNotRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"err":"bad"}`))
	}))
	defer srv.Close()
	s, t2 := newBridgeWithMega(t, srv.URL)
	err := s.sendMegaAPIText(context.Background(), t2, "5511999", "hi")
	require.Error(t, err)
	require.False(t, isRetriable(err))
}

func TestSendMegaAPIText_5xxRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s, t2 := newBridgeWithMega(t, srv.URL)
	err := s.sendMegaAPIText(context.Background(), t2, "5511999", "hi")
	require.Error(t, err)
	require.True(t, isRetriable(err))
}

func TestSendMegaAPIText_2xxOk(t *testing.T) {
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer plain-mega-token", r.Header.Get("Authorization"))
		require.True(t, strings.HasSuffix(r.URL.Path, "/rest/sendMessage/inst-1/text"))
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, t2 := newBridgeWithMega(t, srv.URL)
	require.NoError(t, s.sendMegaAPIText(context.Background(), t2, "5511999", "hi"))
	require.Equal(t, int32(1), got.Load())
}

func TestPostChatwootMessage_SendsExternalID(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "plain-cw-token", r.Header.Get("api_access_token"))
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s, t2 := newBridgeWithCW(t, srv.URL)
	err := s.postChatwootMessage(context.Background(), t2, 99, "hello", "wa-1")
	require.NoError(t, err)
	require.Equal(t, "hello", captured["content"])
	attrs := captured["content_attributes"].(map[string]any)
	require.Equal(t, "wa-1", attrs["external_id"])
}

func TestRunRetryLoop_SucceedsFirstAttempt(t *testing.T) {
	calls := atomic.Int32{}
	err := runRetryLoop(context.Background(), []time.Duration{0, 0, 0}, func() error {
		calls.Add(1)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestRunRetryLoop_RunsFourAttemptsWithThreeBackoffs(t *testing.T) {
	calls := atomic.Int32{}
	err := runRetryLoop(context.Background(), []time.Duration{0, 0, 0}, func() error {
		calls.Add(1)
		return retriable(errors.New("boom"))
	})
	require.Error(t, err)
	require.Equal(t, int32(4), calls.Load())
}

func TestRunRetryLoop_FatalShortCircuits(t *testing.T) {
	calls := atomic.Int32{}
	err := runRetryLoop(context.Background(), []time.Duration{0, 0, 0}, func() error {
		calls.Add(1)
		return notRetriable(errors.New("400"))
	})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestRunRetryLoop_RetriableThenSuccess(t *testing.T) {
	calls := atomic.Int32{}
	err := runRetryLoop(context.Background(), []time.Duration{0, 0, 0}, func() error {
		if calls.Add(1) < 3 {
			return retriable(errors.New("boom"))
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), calls.Load())
}

func TestRunRetryLoop_ContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := atomic.Int32{}
	err := runRetryLoop(ctx, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}, func() error {
		calls.Add(1)
		return retriable(errors.New("boom"))
	})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestCheckBearer_RejectsMismatchAndAcceptsMatch(t *testing.T) {
	key := RandomBytes(32)
	enc, err := Encrypt([]byte("right-token"), key)
	require.NoError(t, err)
	s := &Server{Key: key}
	tn := Tenant{WebhookBearerEnc: enc}

	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"missing header", "", false},
		{"empty bearer", "Bearer ", false},
		{"wrong token", "Bearer wrong-token", false},
		{"right token", "Bearer right-token", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			ok, err := s.checkBearer(req, tn)
			require.NoError(t, err)
			require.Equal(t, c.want, ok)
		})
	}
}

func TestCheckBearer_DecryptErrorSurfaces(t *testing.T) {
	s := &Server{Key: RandomBytes(32)}
	tn := Tenant{WebhookBearerEnc: []byte("not-a-valid-ciphertext")}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer anything")
	_, err := s.checkBearer(req, tn)
	require.Error(t, err)
}

func TestCheckHMAC_DecryptErrorSurfaces(t *testing.T) {
	s := &Server{Key: RandomBytes(32)}
	tn := Tenant{HMACSecretEnc: []byte("not-a-valid-ciphertext")}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := s.checkHMAC(req, tn, []byte(`{}`))
	require.Error(t, err)
}

func newBridgeWithMega(t *testing.T, host string) (*Server, Tenant) {
	t.Helper()
	key := RandomBytes(32)
	enc, err := Encrypt([]byte("plain-mega-token"), key)
	require.NoError(t, err)
	s := &Server{Key: key, Cfg: Config{BufferLimit: 4}}
	tn := Tenant{
		ID:              uuid.New(),
		MegaAPIHost:     host,
		MegaAPIInstance: "inst-1",
		MegaAPITokenEnc: enc,
	}
	return s, tn
}

func newBridgeWithCW(t *testing.T, host string) (*Server, Tenant) {
	t.Helper()
	key := RandomBytes(32)
	enc, err := Encrypt([]byte("plain-cw-token"), key)
	require.NoError(t, err)
	s := &Server{Key: key, Cfg: Config{BufferLimit: 4}}
	tn := Tenant{
		ID:                uuid.New(),
		ChatwootURL:       host,
		ChatwootTokenEnc:  enc,
		ChatwootAccountID: 1,
		ChatwootInboxID:   2,
	}
	return s, tn
}

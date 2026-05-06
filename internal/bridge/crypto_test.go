package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncrypt_RoundtripWithSameKey(t *testing.T) {
	key := RandomBytes(32)
	plain := []byte("hello secret world")
	ct, err := Encrypt(plain, key)
	require.NoError(t, err)
	require.NotEqual(t, plain, ct)
	got, err := Decrypt(ct, key)
	require.NoError(t, err)
	require.Equal(t, plain, got)
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	k1, k2 := RandomBytes(32), RandomBytes(32)
	ct, err := Encrypt([]byte("secret"), k1)
	require.NoError(t, err)
	_, err = Decrypt(ct, k2)
	require.Error(t, err)
}

func TestDecrypt_TamperedFails(t *testing.T) {
	key := RandomBytes(32)
	ct, err := Encrypt([]byte("secret"), key)
	require.NoError(t, err)
	ct[len(ct)-1] ^= 0x01
	_, err = Decrypt(ct, key)
	require.Error(t, err)
}

func TestDecrypt_TruncatedFails(t *testing.T) {
	key := RandomBytes(32)
	_, err := Decrypt([]byte("short"), key)
	require.Error(t, err)
}

func TestEncrypt_RejectsBadKey(t *testing.T) {
	_, err := Encrypt([]byte("x"), []byte("too-short"))
	require.Error(t, err)
}

func TestVerifyHMAC_ValidAccepted(t *testing.T) {
	body := []byte(`{"event":"x"}`)
	secret := "super-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	require.True(t, VerifyHMAC(body, sig, secret))
}

func TestVerifyHMAC_InvalidRejected(t *testing.T) {
	require.False(t, VerifyHMAC([]byte("x"), "deadbeef", "secret"))
	require.False(t, VerifyHMAC([]byte("x"), "not-hex", "secret"))
}

func TestRandomBytes_Length(t *testing.T) {
	a := RandomBytes(32)
	b := RandomBytes(32)
	require.Len(t, a, 32)
	require.Len(t, b, 32)
	require.NotEqual(t, a, b)
}

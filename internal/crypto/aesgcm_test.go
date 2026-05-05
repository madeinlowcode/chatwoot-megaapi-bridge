package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := mustKey(t)
	plaintext := []byte("super-secret-token-1234567890")

	ct, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	if len(ct) <= nonceSize {
		t.Fatalf("ciphertext too short: %d", len(ct))
	}
	got, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q want %q", got, plaintext)
	}
}

func TestEncryptNonceUnique(t *testing.T) {
	key := mustKey(t)
	pt := []byte("abc")
	c1, _ := Encrypt(pt, key)
	c2, _ := Encrypt(pt, key)
	if bytes.Equal(c1, c2) {
		t.Fatal("nonces collided — Encrypt must randomize nonce")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	k1, k2 := mustKey(t), mustKey(t)
	ct, _ := Encrypt([]byte("hello"), k1)
	if _, err := Decrypt(ct, k2); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptTampered(t *testing.T) {
	key := mustKey(t)
	ct, _ := Encrypt([]byte("hello"), key)
	ct[len(ct)-1] ^= 0xff
	if _, err := Decrypt(ct, key); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}

func TestEncryptInvalidKey(t *testing.T) {
	if _, err := Encrypt([]byte("x"), []byte("short")); err == nil {
		t.Fatal("expected ErrInvalidKey")
	}
}

func TestDecryptShort(t *testing.T) {
	key := mustKey(t)
	if _, err := Decrypt([]byte{1, 2, 3}, key); err == nil {
		t.Fatal("expected ErrCiphertextTooShort")
	}
}

func TestKeystoreRoundtrip(t *testing.T) {
	key := mustKey(t)
	ks, err := NewKeystore(CurrentKID, key)
	if err != nil {
		t.Fatal(err)
	}
	ct, kid, err := ks.EncryptToken([]byte("api-token"))
	if err != nil {
		t.Fatal(err)
	}
	if kid != CurrentKID {
		t.Fatalf("kid = %d want %d", kid, CurrentKID)
	}
	pt, err := ks.DecryptToken(ct, kid)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "api-token" {
		t.Fatalf("got %q", pt)
	}
}

func TestKeystoreUnknownKID(t *testing.T) {
	key := mustKey(t)
	ks, _ := NewKeystore(1, key)
	if _, err := ks.DecryptToken([]byte("x"), 99); err == nil {
		t.Fatal("expected ErrUnknownKID")
	}
}

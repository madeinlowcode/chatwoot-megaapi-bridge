package crypto

import "testing"

func TestVerifyHMACValid(t *testing.T) {
	body := []byte(`{"event":"message_created"}`)
	secret := "shared-secret-32-bytes-long-yes!"
	sig := SignHMAC(body, secret)
	if !VerifyHMAC(body, sig, secret) {
		t.Fatal("expected valid signature")
	}
}

func TestVerifyHMACInvalid(t *testing.T) {
	body := []byte("payload")
	if VerifyHMAC(body, "deadbeef", "secret") {
		t.Fatal("expected invalid signature to fail")
	}
}

func TestVerifyHMACEmpty(t *testing.T) {
	if VerifyHMAC([]byte("x"), "", "secret") {
		t.Fatal("empty signature must not validate")
	}
	if VerifyHMAC([]byte("x"), "abc", "") {
		t.Fatal("empty secret must not validate")
	}
}

func TestVerifyHMACTamperedBody(t *testing.T) {
	secret := "k"
	sig := SignHMAC([]byte("a"), secret)
	if VerifyHMAC([]byte("b"), sig, secret) {
		t.Fatal("signature must not validate against different body")
	}
}

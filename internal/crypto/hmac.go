package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// VerifyHMAC validates a hex-encoded HMAC-SHA256 signature against body using secret.
// Comparison is constant-time.
func VerifyHMAC(body []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// SignHMAC produces the hex-encoded HMAC-SHA256 of body using secret. Helpful for tests
// and when the bridge needs to sign outgoing payloads (rare in MVP).
func SignHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

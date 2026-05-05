package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const (
	keySize   = 32 // AES-256
	nonceSize = 12 // GCM standard
)

var (
	ErrInvalidKey        = errors.New("crypto: master key must be 32 bytes")
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")
)

// Encrypt seals plaintext using AES-256-GCM. Output layout: nonce || ciphertext_with_tag.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens ciphertext produced by Encrypt.
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	if len(ciphertext) < aead.NonceSize() {
		return nil, ErrCiphertextTooShort
	}
	nonce := ciphertext[:aead.NonceSize()]
	ct := ciphertext[aead.NonceSize():]
	return aead.Open(nil, nonce, ct, nil)
}

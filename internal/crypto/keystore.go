package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
)

const (
	// CurrentKID is the active key id. Bumped on key rotation.
	CurrentKID int16 = 1

	envMasterKey = "MASTER_KEY"
)

var (
	ErrMasterKeyMissing = errors.New("crypto: MASTER_KEY env var missing or empty")
	ErrUnknownKID       = errors.New("crypto: unknown key id")
)

// Keystore holds the master keys keyed by kid for envelope encryption.
type Keystore struct {
	mu   sync.RWMutex
	keys map[int16][]byte
}

// LoadKeystoreFromEnv reads MASTER_KEY (base64-encoded 32 bytes) and returns a Keystore.
func LoadKeystoreFromEnv() (*Keystore, error) {
	raw := os.Getenv(envMasterKey)
	if raw == "" {
		return nil, ErrMasterKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode MASTER_KEY: %w", err)
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	return &Keystore{keys: map[int16][]byte{CurrentKID: key}}, nil
}

// NewKeystore builds a keystore with a single key. Useful for tests.
func NewKeystore(kid int16, key []byte) (*Keystore, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	return &Keystore{keys: map[int16][]byte{kid: append([]byte(nil), key...)}}, nil
}

// EncryptToken encrypts plaintext with the current key.
func (k *Keystore) EncryptToken(plaintext []byte) ([]byte, int16, error) {
	k.mu.RLock()
	key, ok := k.keys[CurrentKID]
	k.mu.RUnlock()
	if !ok {
		return nil, 0, ErrUnknownKID
	}
	ct, err := Encrypt(plaintext, key)
	if err != nil {
		return nil, 0, err
	}
	return ct, CurrentKID, nil
}

// DecryptToken decrypts ciphertext using the key identified by kid.
func (k *Keystore) DecryptToken(ciphertext []byte, kid int16) ([]byte, error) {
	k.mu.RLock()
	key, ok := k.keys[kid]
	k.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKID
	}
	return Decrypt(ciphertext, key)
}

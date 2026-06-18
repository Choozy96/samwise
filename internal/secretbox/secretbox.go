// Package secretbox is the single chokepoint for encrypting secrets stored in
// the SQLite DB: MCP credentials, API tokens, service-account JSON.
//
// Everything is AES-256-GCM under the bootstrap MASTER_KEY. Because the key
// lives outside the DB (in the env file), DB dumps and backups are safe to move.
// Keeping all encryption behind this one type means a future KMS/Vault swap is a
// localized change.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrNoKey is returned when an encrypt/decrypt is attempted but no MASTER_KEY
// was configured.
var ErrNoKey = errors.New("secretbox: MASTER_KEY not configured")

// Box performs authenticated encryption with a fixed 32-byte key.
type Box struct {
	aead cipher.AEAD
}

// New constructs a Box from a 32-byte key. A nil/short key yields a Box whose
// Encrypt/Decrypt return ErrNoKey, so callers can construct one unconditionally
// and only fail when they actually touch a secret.
func New(key []byte) (*Box, error) {
	if len(key) == 0 {
		return &Box{}, nil
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Enabled reports whether a usable key was provided.
func (b *Box) Enabled() bool { return b.aead != nil }

// Encrypt seals plaintext and returns a base64 string (nonce||ciphertext),
// suitable for storing in a TEXT column.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	if b.aead == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(encoded string) ([]byte, error) {
	if b.aead == nil {
		return nil, ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secretbox: bad base64: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: decrypt failed: %w", err)
	}
	return pt, nil
}

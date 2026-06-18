package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func key32(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

// TestRoundTrip seals and opens a value under the same key.
func TestRoundTrip(t *testing.T) {
	b, err := New(key32(1))
	if err != nil {
		t.Fatal(err)
	}
	if !b.Enabled() {
		t.Fatal("box with a 32-byte key should be Enabled")
	}
	plain := []byte("super-secret-api-token")
	sealed, err := b.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(sealed), plain) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	got, err := b.Decrypt(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
	}
}

// TestNonceIsRandom: encrypting the same plaintext twice yields different
// ciphertext (fresh nonce each time), so identical secrets aren't linkable.
func TestNonceIsRandom(t *testing.T) {
	b, _ := New(key32(2))
	a, _ := b.Encrypt([]byte("same"))
	c, _ := b.Encrypt([]byte("same"))
	if a == c {
		t.Fatal("two encryptions of the same plaintext must differ")
	}
}

// TestWrongKeyFails: a value sealed under one key cannot be opened with another.
func TestWrongKeyFails(t *testing.T) {
	enc, _ := New(key32(3))
	dec, _ := New(key32(4))
	sealed, _ := enc.Encrypt([]byte("secret"))
	if _, err := dec.Decrypt(sealed); err == nil {
		t.Fatal("decrypt with the wrong key must fail")
	}
}

// TestTamperDetected: flipping a ciphertext byte fails the GCM auth tag.
func TestTamperDetected(t *testing.T) {
	b, _ := New(key32(5))
	sealed, _ := b.Encrypt([]byte("integrity matters"))
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 0xFF // corrupt the tag
	if _, err := b.Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext must not decrypt")
	}
}

// TestDisabledBox: a box with no key construct successfully but refuses crypto
// with ErrNoKey, so callers can build one unconditionally.
func TestDisabledBox(t *testing.T) {
	b, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Enabled() {
		t.Fatal("box with no key should be disabled")
	}
	if _, err := b.Encrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Encrypt without a key should return ErrNoKey, got %v", err)
	}
	if _, err := b.Decrypt("AAAA"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Decrypt without a key should return ErrNoKey, got %v", err)
	}
}

// TestBadKeyLength: a key that isn't 32 bytes is rejected at construction.
func TestBadKeyLength(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("a 16-byte key must be rejected")
	}
}

// TestDecryptGarbage: malformed/short inputs error instead of panicking.
func TestDecryptGarbage(t *testing.T) {
	b, _ := New(key32(6))
	if _, err := b.Decrypt("!!!not base64!!!"); err == nil {
		t.Fatal("non-base64 input should error")
	}
	if _, err := b.Decrypt(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("a ciphertext shorter than the nonce should error")
	}
}

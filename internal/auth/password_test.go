package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestHashVerifyRoundTrip: the right password verifies, a wrong one returns
// ErrMismatch (not a generic error, so callers can distinguish bad-password
// from malformed-hash).
func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected a PHC argon2id hash, got %q", hash)
	}
	if err := VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Fatalf("correct password should verify, got %v", err)
	}
	if err := VerifyPassword("wrong password", hash); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong password should return ErrMismatch, got %v", err)
	}
}

// TestSaltIsRandom: hashing the same password twice yields different hashes, so
// equal passwords aren't visible as equal hashes in a DB dump.
func TestSaltIsRandom(t *testing.T) {
	a, _ := HashPassword("same-password")
	b, _ := HashPassword("same-password")
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
	// Both must still verify.
	if err := VerifyPassword("same-password", a); err != nil {
		t.Errorf("hash a should verify: %v", err)
	}
	if err := VerifyPassword("same-password", b); err != nil {
		t.Errorf("hash b should verify: %v", err)
	}
}

// TestVerifyMalformedHash: a non-argon2id / corrupt hash is reported as a format
// error, distinct from a password mismatch.
func TestVerifyMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$broken", "$bcrypt$v=1$x$y$z"} {
		err := VerifyPassword("whatever", bad)
		if err == nil {
			t.Errorf("malformed hash %q should error", bad)
		}
		if errors.Is(err, ErrMismatch) {
			t.Errorf("malformed hash %q should not be reported as a mismatch", bad)
		}
	}
}

// TestEmptyPassword: an empty password still round-trips and is not confused
// with a non-empty one.
func TestEmptyPassword(t *testing.T) {
	h, err := HashPassword("")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword("", h); err != nil {
		t.Errorf("empty password should verify against its own hash: %v", err)
	}
	if err := VerifyPassword("x", h); !errors.Is(err, ErrMismatch) {
		t.Errorf("non-empty password must not match the empty-password hash, got %v", err)
	}
}

package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// NewSecret generates a random secret, returning the plaintext (to hand to the
// client exactly once) and its SHA-256 hex digest (to store).
func NewSecret() (plain, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, HashSecret(plain)
}

// HashSecret returns the SHA-256 hex digest of a secret.
func HashSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifySecret reports whether plain hashes to hash, in constant time.
func VerifySecret(plain, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashSecret(plain)), []byte(hash)) == 1
}

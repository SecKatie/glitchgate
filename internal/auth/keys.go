// Package auth provides proxy key generation, hashing, and session management.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	keyPrefix  = "llmp_sk_"
	keyBytes   = 32
	bcryptCost = 10
	// prefixLen is the number of characters from the full key to use as the
	// human-readable prefix (e.g. "llmp_sk_ab12").
	prefixLen = 12
)

// GenerateKey creates a new proxy API key.  It returns the plaintext key
// (for display to the user once), the bcrypt hash (for storage), and a
// short prefix (for identification in logs/UI).
func GenerateKey() (plaintext, hash, prefix string, err error) {
	raw := make([]byte, keyBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generating random bytes: %w", err)
	}

	plaintext = keyPrefix + hex.EncodeToString(raw)
	prefix = plaintext[:prefixLen]

	hash, err = HashKey(plaintext)
	if err != nil {
		return "", "", "", err
	}

	return plaintext, hash, prefix, nil
}

// HashKey returns the bcrypt hash of a plaintext API key.
func HashKey(plaintext string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing key: %w", err)
	}

	return string(h), nil
}

// VerifyKey reports whether a plaintext API key matches the given bcrypt
// hash.
func VerifyKey(plaintext, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

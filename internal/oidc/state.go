// SPDX-License-Identifier: AGPL-3.0-or-later

package oidc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateState returns a cryptographically random 32-byte hex string for use
// as the OIDC state parameter (CSRF protection).
func GenerateState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate oidc state: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// GeneratePKCEPair returns a code_verifier and the corresponding S256
// code_challenge for use in the PKCE extension.
func GeneratePKCEPair() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate pkce pair: %w", err)
	}
	verifier = hex.EncodeToString(raw)
	challenge = pkceS256Challenge(verifier)
	return verifier, challenge, nil
}

// pkceS256Challenge computes the S256 code_challenge from a verifier.
// BASE64URL(SHA256(ASCII(code_verifier))) per RFC 7636 §4.2.
func pkceS256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

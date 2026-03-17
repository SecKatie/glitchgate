package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateState(t *testing.T) {
	s, err := GenerateState()
	require.NoError(t, err)
	require.Len(t, s, 64, "state should be 32 bytes hex-encoded (64 chars)")

	// Two calls should produce different values.
	s2, err := GenerateState()
	require.NoError(t, err)
	require.NotEqual(t, s, s2)
}

func TestGeneratePKCEPair(t *testing.T) {
	verifier, challenge, err := GeneratePKCEPair()
	require.NoError(t, err)
	require.Len(t, verifier, 64, "verifier should be 32 bytes hex-encoded")
	require.NotEmpty(t, challenge)

	// Challenge should be deterministic from verifier.
	require.Equal(t, pkceS256Challenge(verifier), challenge)
}

func TestPKCEChallenge_Deterministic(t *testing.T) {
	verifier := "deadbeef01234567890abcdef01234567890abcdef01234567890abcdef0123"
	c1 := pkceS256Challenge(verifier)
	c2 := pkceS256Challenge(verifier)
	require.Equal(t, c1, c2, "challenge should be deterministic")

	// Verify against manually computed value.
	h := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(h[:])
	require.Equal(t, expected, c1)
}

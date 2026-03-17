package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateKey(t *testing.T) {
	plaintext, hash, prefix, err := GenerateKey()
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(plaintext, keyPrefix), "key should have prefix %q", keyPrefix)
	require.Equal(t, plaintext[:prefixLen], prefix)
	require.NotEmpty(t, hash)
	require.True(t, VerifyKey(plaintext, hash), "generated hash should verify against plaintext")
}

func TestGenerateKey_Unique(t *testing.T) {
	p1, _, _, err := GenerateKey()
	require.NoError(t, err)
	p2, _, _, err := GenerateKey()
	require.NoError(t, err)
	require.NotEqual(t, p1, p2, "two generated keys should differ")
}

func TestHashKey(t *testing.T) {
	hash, err := HashKey("test-key")
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	// bcrypt hashes start with $2a$ or $2b$
	require.True(t, strings.HasPrefix(hash, "$2"), "hash should be bcrypt format")
}

func TestVerifyKey(t *testing.T) {
	plaintext := "llmp_sk_test1234567890abcdef"
	hash, err := HashKey(plaintext)
	require.NoError(t, err)

	tests := []struct {
		name    string
		key     string
		matches bool
	}{
		{"correct key", plaintext, true},
		{"wrong key", "llmp_sk_wrong_key_value_here", false},
		{"empty key", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.matches, VerifyKey(tc.key, hash))
		})
	}
}

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/store"
)

// mockSessionStore is a minimal in-memory implementation of store.SessionBackendStore for tests.
type mockSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*store.UISession // keyed by token (hash)
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{sessions: make(map[string]*store.UISession)}
}

func (m *mockSessionStore) CreateUISession(_ context.Context, id, token, sessionType, userID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := &store.UISession{
		ID:          id,
		Token:       token,
		SessionType: sessionType,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
	}
	if userID != "" {
		sess.UserID = &userID
	}
	m.sessions[token] = sess
	return nil
}

func (m *mockSessionStore) GetUISessionByToken(_ context.Context, token string) (*store.UISession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[token]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}
	return sess, nil
}

func (m *mockSessionStore) DeleteUISession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

func (m *mockSessionStore) DeleteUISessionsByUserID(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.UserID != nil && *s.UserID == userID {
			delete(m.sessions, k)
		}
	}
	return nil
}

func (m *mockSessionStore) CleanupExpiredSessions(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, s := range m.sessions {
		if now.After(s.ExpiresAt) {
			delete(m.sessions, k)
		}
	}
	return nil
}

func TestHashSessionToken(t *testing.T) {
	token := "abc123def456"
	h := hashSessionToken(token)
	require.Len(t, h, 64, "SHA-256 hex digest should be 64 chars")

	// Verify deterministic.
	require.Equal(t, h, hashSessionToken(token))

	// Verify against known vector.
	expected := sha256.Sum256([]byte(token))
	require.Equal(t, hex.EncodeToString(expected[:]), h)
}

func TestCreate(t *testing.T) {
	ms := newMockSessionStore()
	ss := NewUISessionStore(ms)
	ctx := context.Background()

	sess, err := ss.Create(ctx, "master_key", "")
	require.NoError(t, err)
	require.NotEmpty(t, sess.ID)
	require.NotEmpty(t, sess.Token)
	require.Equal(t, "master_key", sess.SessionType)
	require.Nil(t, sess.UserID)

	// The stored token should be the hash, not the plaintext.
	hashed := hashSessionToken(sess.Token)
	ms.mu.Lock()
	_, hasHash := ms.sessions[hashed]
	_, hasPlain := ms.sessions[sess.Token]
	ms.mu.Unlock()
	require.True(t, hasHash, "DB should store hashed token")
	require.False(t, hasPlain, "DB should not store plaintext token")
}

func TestCreate_WithUserID(t *testing.T) {
	ms := newMockSessionStore()
	ss := NewUISessionStore(ms)
	ctx := context.Background()

	sess, err := ss.Create(ctx, "oidc", "user-42")
	require.NoError(t, err)
	require.NotNil(t, sess.UserID)
	require.Equal(t, "user-42", *sess.UserID)
}

func TestValidate(t *testing.T) {
	ms := newMockSessionStore()
	ss := NewUISessionStore(ms)
	ctx := context.Background()

	sess, err := ss.Create(ctx, "master_key", "")
	require.NoError(t, err)

	// Valid token succeeds.
	got, err := ss.Validate(ctx, sess.Token)
	require.NoError(t, err)
	require.Equal(t, sess.ID, got.ID)

	// Wrong token fails.
	_, err = ss.Validate(ctx, "wrong-token-value")
	require.Error(t, err)
}

func TestDelete(t *testing.T) {
	ms := newMockSessionStore()
	ss := NewUISessionStore(ms)
	ctx := context.Background()

	sess, err := ss.Create(ctx, "master_key", "")
	require.NoError(t, err)

	require.NoError(t, ss.Delete(ctx, sess.Token))

	// Should no longer validate.
	_, err = ss.Validate(ctx, sess.Token)
	require.Error(t, err)
}

func TestCleanup(t *testing.T) {
	ms := newMockSessionStore()
	ss := NewUISessionStore(ms)
	ctx := context.Background()

	sess, err := ss.Create(ctx, "master_key", "")
	require.NoError(t, err)

	// Force expiration.
	hashed := hashSessionToken(sess.Token)
	ms.mu.Lock()
	ms.sessions[hashed].ExpiresAt = time.Now().UTC().Add(-time.Hour)
	ms.mu.Unlock()

	require.NoError(t, ss.Cleanup(ctx))

	ms.mu.Lock()
	count := len(ms.sessions)
	ms.mu.Unlock()
	require.Equal(t, 0, count, "expired session should be cleaned up")
}

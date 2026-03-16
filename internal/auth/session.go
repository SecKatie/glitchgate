// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/seckatie/glitchgate/internal/store"
)

const (
	sessionTokenBytes = 32
	sessionTTL        = 8 * time.Hour
)

// UISessionStore manages UI sessions backed by the database.
type UISessionStore struct {
	store store.SessionBackendStore
}

// NewUISessionStore creates a UISessionStore backed by the given store.
func NewUISessionStore(s store.SessionBackendStore) *UISessionStore {
	return &UISessionStore{store: s}
}

// Create generates a new session of the given type and persists it.
// userID is empty for master_key sessions.
func (s *UISessionStore) Create(ctx context.Context, sessionType, userID string) (*store.UISession, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	id := uuid.New().String()
	expiresAt := time.Now().UTC().Add(sessionTTL)

	if err := s.store.CreateUISession(ctx, id, token, sessionType, userID, expiresAt); err != nil {
		return nil, fmt.Errorf("persist ui session: %w", err)
	}

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
	return sess, nil
}

// Validate checks the token and returns the session if valid.
func (s *UISessionStore) Validate(ctx context.Context, token string) (*store.UISession, error) {
	return s.store.GetUISessionByToken(ctx, token)
}

// Delete removes a session by token (logout).
func (s *UISessionStore) Delete(ctx context.Context, token string) error {
	return s.store.DeleteUISession(ctx, token)
}

// DeleteAllForUser removes all sessions for a given user (deactivation).
func (s *UISessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	return s.store.DeleteUISessionsByUserID(ctx, userID)
}

// Cleanup removes expired sessions.
func (s *UISessionStore) Cleanup(ctx context.Context) error {
	return s.store.CleanupExpiredSessions(ctx)
}

// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const sessionTokenBytes = 32

// Session represents an authenticated UI session.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore is an in-memory store for UI sessions with automatic expiry.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

// NewSessionStore creates a SessionStore with the given session lifetime.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

// Create generates a new session with a cryptographically random token.
func (s *SessionStore) Create() (*Session, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sess := &Session{
		Token:     hex.EncodeToString(raw),
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	s.sessions[sess.Token] = sess
	s.mu.Unlock()

	return sess, nil
}

// Validate checks whether the given token refers to an active (non-expired)
// session.
func (s *SessionStore) Validate(token string) bool {
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()

	if !ok {
		return false
	}

	return time.Now().UTC().Before(sess.ExpiresAt)
}

// Delete removes a session by token.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// Cleanup removes all expired sessions.
func (s *SessionStore) Cleanup() {
	now := time.Now().UTC()

	s.mu.Lock()
	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
}

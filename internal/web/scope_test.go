// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

func TestVisibleKeyScope(t *testing.T) {
	t.Run("team admin without team falls back to user scope", func(t *testing.T) {
		sc := &auth.UISessionContext{
			SessionID: "sess-123",
			Role:      "team_admin",
			User:      &store.OIDCUser{ID: "user-123"},
		}

		scope := visibleKeyScope(sc)

		require.Equal(t, "user", scope.scopeType)
		require.Equal(t, "user-123", scope.scopeUserID)
		require.Empty(t, scope.scopeTeamID)
	})

	t.Run("team admin with team uses team scope", func(t *testing.T) {
		teamID := "team-123"
		sc := &auth.UISessionContext{
			SessionID: "sess-123",
			Role:      "team_admin",
			User:      &store.OIDCUser{ID: "user-123"},
			TeamID:    &teamID,
		}

		scope := visibleKeyScope(sc)

		require.Equal(t, "team", scope.scopeType)
		require.Empty(t, scope.scopeUserID)
		require.Equal(t, "team-123", scope.scopeTeamID)
	})

	t.Run("team admin without team or user returns none", func(t *testing.T) {
		sc := &auth.UISessionContext{
			SessionID: "sess-123",
			Role:      "team_admin",
		}

		scope := visibleKeyScope(sc)

		require.Equal(t, "none", scope.scopeType)
	})

	t.Run("member without user returns none", func(t *testing.T) {
		sc := &auth.UISessionContext{
			SessionID: "sess-123",
			Role:      "member",
		}

		scope := visibleKeyScope(sc)

		require.Equal(t, "none", scope.scopeType)
	})
}

type keyScopeStoreStub struct {
	store.Store
	activeKeys []store.ProxyKeySummary
	ownerKeys  []store.ProxyKeySummary
	teamKeys   []store.ProxyKeySummary

	ownerCalls       []string
	teamCalls        []string
	updateLabelCalls []string
	revokeCalls      []string
	auditCalls       []string
}

func (s *keyScopeStoreStub) ListActiveProxyKeys(context.Context) ([]store.ProxyKeySummary, error) {
	return s.activeKeys, nil
}

func (s *keyScopeStoreStub) ListProxyKeysByOwner(_ context.Context, ownerUserID string) ([]store.ProxyKeySummary, error) {
	s.ownerCalls = append(s.ownerCalls, ownerUserID)
	return s.ownerKeys, nil
}

func (s *keyScopeStoreStub) ListProxyKeysByTeam(_ context.Context, teamID string) ([]store.ProxyKeySummary, error) {
	s.teamCalls = append(s.teamCalls, teamID)
	return s.teamKeys, nil
}

func (s *keyScopeStoreStub) UpdateKeyLabel(_ context.Context, prefix, label string) error {
	s.updateLabelCalls = append(s.updateLabelCalls, prefix+":"+label)
	return nil
}

func (s *keyScopeStoreStub) RevokeProxyKey(_ context.Context, prefix string) error {
	s.revokeCalls = append(s.revokeCalls, prefix)
	return nil
}

func (s *keyScopeStoreStub) RecordAuditEvent(_ context.Context, action, keyPrefix, _, _ string) error {
	s.auditCalls = append(s.auditCalls, action+":"+keyPrefix)
	return nil
}

func TestListKeysForSessionUsesScopedIdentity(t *testing.T) {
	stub := &keyScopeStoreStub{
		ownerKeys: []store.ProxyKeySummary{{KeyPrefix: "llmp_sk_user"}},
	}
	h := &Handlers{store: stub}

	req := httptest.NewRequest("GET", "/ui/keys", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
		SessionID: "sess-123",
		Role:      "team_admin",
		User:      &store.OIDCUser{ID: "user-123"},
	}))

	keys, err := h.listKeysForSession(req)

	require.NoError(t, err)
	require.Equal(t, []store.ProxyKeySummary{{KeyPrefix: "llmp_sk_user"}}, keys)
	require.Equal(t, []string{"user-123"}, stub.ownerCalls)
	require.Empty(t, stub.teamCalls)
}

func TestListKeysForSessionNoneScope(t *testing.T) {
	stub := &keyScopeStoreStub{
		activeKeys: []store.ProxyKeySummary{{KeyPrefix: "llmp_sk_leak"}},
	}
	h := &Handlers{store: stub}

	req := httptest.NewRequest("GET", "/ui/keys", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
		SessionID: "sess-123",
		Role:      "member",
		// User is nil — triggers "none" scope
	}))

	keys, err := h.listKeysForSession(req)

	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestCanMutateKey(t *testing.T) {
	ownedKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_own1"}
	otherKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_othe"}

	tests := []struct {
		name    string
		session *auth.UISessionContext
		prefix  string
		keys    []store.ProxyKeySummary
		allowed bool
	}{
		{
			name:    "master key can mutate any key",
			session: &auth.UISessionContext{IsMasterKey: true},
			prefix:  otherKey.KeyPrefix,
			allowed: true,
		},
		{
			name:    "global admin can mutate any key",
			session: &auth.UISessionContext{Role: "global_admin", User: &store.OIDCUser{ID: "admin-1"}},
			prefix:  otherKey.KeyPrefix,
			allowed: true,
		},
		{
			name:    "nil session can mutate any key",
			session: nil,
			prefix:  otherKey.KeyPrefix,
			allowed: true,
		},
		{
			name:    "member can mutate own key",
			session: &auth.UISessionContext{Role: "member", User: &store.OIDCUser{ID: "user-1"}},
			prefix:  ownedKey.KeyPrefix,
			keys:    []store.ProxyKeySummary{ownedKey},
			allowed: true,
		},
		{
			name:    "member cannot mutate other key",
			session: &auth.UISessionContext{Role: "member", User: &store.OIDCUser{ID: "user-1"}},
			prefix:  otherKey.KeyPrefix,
			keys:    []store.ProxyKeySummary{ownedKey},
			allowed: false,
		},
		{
			name: "team admin can mutate team key",
			session: func() *auth.UISessionContext {
				tid := "team-1"
				return &auth.UISessionContext{Role: "team_admin", User: &store.OIDCUser{ID: "user-1"}, TeamID: &tid}
			}(),
			prefix:  ownedKey.KeyPrefix,
			keys:    []store.ProxyKeySummary{ownedKey},
			allowed: true,
		},
		{
			name: "team admin cannot mutate key outside team",
			session: func() *auth.UISessionContext {
				tid := "team-1"
				return &auth.UISessionContext{Role: "team_admin", User: &store.OIDCUser{ID: "user-1"}, TeamID: &tid}
			}(),
			prefix:  otherKey.KeyPrefix,
			keys:    []store.ProxyKeySummary{ownedKey},
			allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &keyScopeStoreStub{
				ownerKeys: tt.keys,
				teamKeys:  tt.keys,
			}
			h := &Handlers{store: stub}

			req := httptest.NewRequest("GET", "/", nil)
			if tt.session != nil {
				req = req.WithContext(auth.ContextWithSession(req.Context(), tt.session))
			}

			allowed, err := h.canMutateKey(req, tt.prefix)
			require.NoError(t, err)
			require.Equal(t, tt.allowed, allowed)
		})
	}
}

func TestUpdateKeyLabelHandler(t *testing.T) {
	ownedKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_own1"}
	otherKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_othe"}

	t.Run("member can update own key label", func(t *testing.T) {
		stub := &keyScopeStoreStub{ownerKeys: []store.ProxyKeySummary{ownedKey}}
		h := &Handlers{store: stub}

		form := url.Values{"label": {"new-label"}}
		req := httptest.NewRequest("POST", "/ui/api/keys/"+ownedKey.KeyPrefix+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			Role: "member",
			User: &store.OIDCUser{ID: "user-1"},
		}))
		w := httptest.NewRecorder()

		h.UpdateKeyLabelHandler(w, req, ownedKey.KeyPrefix)

		require.NotEqual(t, 403, w.Code)
		require.Equal(t, []string{ownedKey.KeyPrefix + ":new-label"}, stub.updateLabelCalls)
	})

	t.Run("member cannot update other key label", func(t *testing.T) {
		stub := &keyScopeStoreStub{ownerKeys: []store.ProxyKeySummary{ownedKey}}
		h := &Handlers{store: stub}

		form := url.Values{"label": {"new-label"}}
		req := httptest.NewRequest("POST", "/ui/api/keys/"+otherKey.KeyPrefix+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			Role: "member",
			User: &store.OIDCUser{ID: "user-1"},
		}))
		w := httptest.NewRecorder()

		h.UpdateKeyLabelHandler(w, req, otherKey.KeyPrefix)

		require.Equal(t, 403, w.Code)
		require.Empty(t, stub.updateLabelCalls)
	})

	t.Run("global admin can update any key label", func(t *testing.T) {
		stub := &keyScopeStoreStub{activeKeys: []store.ProxyKeySummary{otherKey}}
		h := &Handlers{store: stub}

		form := url.Values{"label": {"admin-label"}}
		req := httptest.NewRequest("POST", "/ui/api/keys/"+otherKey.KeyPrefix+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			Role: "global_admin",
			User: &store.OIDCUser{ID: "admin-1"},
		}))
		w := httptest.NewRecorder()

		h.UpdateKeyLabelHandler(w, req, otherKey.KeyPrefix)

		require.NotEqual(t, 403, w.Code)
		require.Equal(t, []string{otherKey.KeyPrefix + ":admin-label"}, stub.updateLabelCalls)
	})

	t.Run("master key can update any key label", func(t *testing.T) {
		stub := &keyScopeStoreStub{}
		h := &Handlers{store: stub}

		form := url.Values{"label": {"master-label"}}
		req := httptest.NewRequest("POST", "/ui/api/keys/"+otherKey.KeyPrefix+"/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			IsMasterKey: true,
		}))
		w := httptest.NewRecorder()

		h.UpdateKeyLabelHandler(w, req, otherKey.KeyPrefix)

		require.NotEqual(t, 403, w.Code)
		require.Equal(t, []string{otherKey.KeyPrefix + ":master-label"}, stub.updateLabelCalls)
	})
}

func TestRevokeKeyHandler(t *testing.T) {
	ownedKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_own1"}
	otherKey := store.ProxyKeySummary{KeyPrefix: "llmp_sk_othe"}

	t.Run("member can revoke own key", func(t *testing.T) {
		stub := &keyScopeStoreStub{ownerKeys: []store.ProxyKeySummary{ownedKey}}
		h := &Handlers{store: stub}

		req := httptest.NewRequest("POST", "/ui/api/keys/"+ownedKey.KeyPrefix+"/revoke", nil)
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			Role: "member",
			User: &store.OIDCUser{ID: "user-1"},
		}))
		w := httptest.NewRecorder()

		h.RevokeKeyHandler(w, req, ownedKey.KeyPrefix)

		require.NotEqual(t, 403, w.Code)
		require.Equal(t, []string{ownedKey.KeyPrefix}, stub.revokeCalls)
	})

	t.Run("member cannot revoke other key", func(t *testing.T) {
		stub := &keyScopeStoreStub{ownerKeys: []store.ProxyKeySummary{ownedKey}}
		h := &Handlers{store: stub}

		req := httptest.NewRequest("POST", "/ui/api/keys/"+otherKey.KeyPrefix+"/revoke", nil)
		req = req.WithContext(auth.ContextWithSession(req.Context(), &auth.UISessionContext{
			Role: "member",
			User: &store.OIDCUser{ID: "user-1"},
		}))
		w := httptest.NewRecorder()

		h.RevokeKeyHandler(w, req, otherKey.KeyPrefix)

		require.Equal(t, 403, w.Code)
		require.Empty(t, stub.revokeCalls)
	})
}

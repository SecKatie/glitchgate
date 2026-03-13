// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/store"
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
}

type keyScopeStoreStub struct {
	store.Store
	activeKeys []store.ProxyKeySummary
	ownerKeys  []store.ProxyKeySummary
	teamKeys   []store.ProxyKeySummary

	ownerCalls []string
	teamCalls  []string
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

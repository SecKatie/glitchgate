// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

type keyScope struct {
	scopeType   string
	scopeUserID string
	scopeTeamID string
}

func visibleKeyScope(sc *auth.UISessionContext) keyScope {
	scopeType, scopeUserID, scopeTeamID := buildScopeParams(sc)
	return keyScope{
		scopeType:   scopeType,
		scopeUserID: scopeUserID,
		scopeTeamID: scopeTeamID,
	}
}

// buildScopeParams populates the ScopeType/ScopeUserID/ScopeTeamID fields of
// a ListLogsParams based on the current session context.
//
// Rules:
//   - GA or master_key  → ScopeType "all"  (sees everything)
//   - team_admin        → ScopeType "team" (scoped to their team)
//   - member            → ScopeType "user" (own keys only)
func buildScopeParams(sc *auth.UISessionContext) (scopeType, scopeUserID, scopeTeamID string) {
	if sc == nil || sc.IsMasterKey {
		return "all", "", ""
	}
	switch sc.Role {
	case "global_admin":
		return "all", "", ""
	case "team_admin":
		if sc.TeamID != nil {
			return "team", "", *sc.TeamID
		}
		// TA without a team — fall back to own keys.
		if sc.User != nil {
			return "user", sc.User.ID, ""
		}
		return "none", "", ""
	default: // member
		if sc.User != nil {
			return "user", sc.User.ID, ""
		}
		return "none", "", ""
	}
}

// applyScopeToParams sets the scope fields on params using the session context.
func applyScopeToParams(sc *auth.UISessionContext, params *store.ListLogsParams) {
	params.ScopeType, params.ScopeUserID, params.ScopeTeamID = buildScopeParams(sc)
}

// applyScopeToCostParams sets the scope fields on cost params using the session context.
func applyScopeToCostParams(sc *auth.UISessionContext, params *store.CostParams) {
	params.ScopeType, params.ScopeUserID, params.ScopeTeamID = buildScopeParams(sc)
}

// setNavData injects common nav fields (CurrentUser) into a template data map
// from the session context. Call this in every page handler before rendering.
func setNavData(data map[string]any, sc *auth.UISessionContext) {
	if sc == nil {
		return
	}
	if sc.IsMasterKey {
		data["CurrentUser"] = "admin"
	} else if sc.User != nil {
		data["CurrentUser"] = sc.User.DisplayName
	}
}

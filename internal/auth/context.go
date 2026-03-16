// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"

	"github.com/seckatie/glitchgate/internal/store"
)

type contextKey struct{}

// UISessionContext carries the resolved session state for one HTTP request.
type UISessionContext struct {
	SessionID   string
	SessionType string // "oidc" | "master_key"
	IsMasterKey bool
	User        *store.OIDCUser // nil for master_key sessions
	TeamID      *string         // nil if user has no team assignment
	Role        string          // "global_admin" | "team_admin" | "member" | "master_key"
}

// SessionFromContext retrieves the UISessionContext injected by UISessionMiddleware.
// Returns nil if no session has been injected (unauthenticated request).
func SessionFromContext(ctx context.Context) *UISessionContext {
	v, _ := ctx.Value(contextKey{}).(*UISessionContext)
	return v
}

// ContextWithSession returns a new context carrying the given UISessionContext.
func ContextWithSession(ctx context.Context, sess *UISessionContext) context.Context {
	return context.WithValue(ctx, contextKey{}, sess)
}

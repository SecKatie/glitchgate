// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// UISessionMiddleware validates the llmp_session cookie against the database,
// loads the OIDC user (for oidc sessions), and injects a UISessionContext into
// the request context. Unauthenticated requests are redirected to /ui/login.
func UISessionMiddleware(sessions *auth.UISessionStore, st store.SessionReaderStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""

			// Check llmp_session cookie.
			if c, err := r.Cookie("llmp_session"); err == nil {
				token = c.Value
			}

			// Fallback to legacy "session" cookie for in-flight sessions.
			if token == "" {
				if c, err := r.Cookie("session"); err == nil {
					token = c.Value
				}
			}

			// Fallback to Authorization header for API clients.
			if token == "" {
				if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
					token = bearer[7:]
				}
			}

			if token == "" {
				redirectOrUnauthorized(w, r)
				return
			}

			sess, err := sessions.Validate(r.Context(), token)
			if err != nil || sess == nil {
				redirectOrUnauthorized(w, r)
				return
			}

			sc := &auth.UISessionContext{
				SessionID:   sess.ID,
				SessionType: sess.SessionType,
			}

			if sess.SessionType == "master_key" {
				sc.IsMasterKey = true
				sc.Role = "global_admin"
			} else if sess.SessionType == "oidc" && sess.UserID != nil {
				user, err := st.GetOIDCUserByID(r.Context(), *sess.UserID)
				if err != nil {
					slog.Warn("UISessionMiddleware: load user from session", "error", err)
					redirectOrUnauthorized(w, r)
					return
				}
				if user == nil || !user.Active {
					// Deactivated user — invalidate the session and redirect.
					if delErr := sessions.Delete(r.Context(), token); delErr != nil {
						slog.Warn("UISessionMiddleware: delete session", "error", delErr)
					}
					http.Redirect(w, r, "/ui/login?error=deactivated", http.StatusSeeOther)
					return
				}
				sc.User = user
				sc.Role = user.Role

				// Load team membership (best-effort).
				if tm, err := st.GetTeamMembership(r.Context(), user.ID); err == nil && tm != nil {
					sc.TeamID = &tm.TeamID
				}
			}

			next.ServeHTTP(w, r.WithContext(auth.ContextWithSession(r.Context(), sc)))
		})
	}
}

// RequireGlobalAdmin returns 403 unless the session is a global_admin or master_key.
func RequireGlobalAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := auth.SessionFromContext(r.Context())
		if sc == nil || (!sc.IsMasterKey && sc.Role != "global_admin") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminOrTeamAdmin returns 403 unless the session is global_admin, team_admin, or master_key.
func RequireAdminOrTeamAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := auth.SessionFromContext(r.Context())
		if sc == nil || (!sc.IsMasterKey && sc.Role != "global_admin" && sc.Role != "team_admin") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TeamResolver extracts the target team ID from a request. Used by
// RequireTeamScope to determine whether a team_admin is acting within scope.
type TeamResolver func(r *http.Request) (teamID string, err error)

// errTeamNotResolved is returned when a resolver cannot determine the team.
var errTeamNotResolved = errors.New("team not resolved")

// RequireTeamScope enforces that team_admin users can only access resources
// belonging to their own team. Global admins and master_key sessions pass
// through unrestricted. Stack after RequireAdminOrTeamAdmin.
func RequireTeamScope(resolve TeamResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sc := auth.SessionFromContext(r.Context())
			if sc == nil || sc.IsMasterKey || sc.Role != "team_admin" {
				next.ServeHTTP(w, r)
				return
			}

			targetTeamID, err := resolve(r)
			if err != nil || sc.TeamID == nil || *sc.TeamID != targetTeamID {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// TeamIDFromParam returns a TeamResolver that reads the team ID directly from
// a chi URL parameter (for routes where {param} IS the team ID).
func TeamIDFromParam(param string) TeamResolver {
	return func(r *http.Request) (string, error) {
		id := chi.URLParam(r, param)
		if id == "" {
			return "", errTeamNotResolved
		}
		return id, nil
	}
}

// UserTeamResolver returns a TeamResolver that reads {id} as a user ID and
// looks up the user's team membership via the store.
func UserTeamResolver(st store.SessionReaderStore) TeamResolver {
	return func(r *http.Request) (string, error) {
		userID := chi.URLParam(r, "id")
		if userID == "" {
			return "", errTeamNotResolved
		}
		tm, err := st.GetTeamMembership(r.Context(), userID)
		if err != nil || tm == nil {
			return "", errTeamNotResolved
		}
		return tm.TeamID, nil
	}
}

// KeyTeamResolver returns a TeamResolver that reads {id} as a proxy key ID and
// verifies the key belongs to the caller's team. It resolves to the caller's
// team ID only if the key is found within that team's key set.
func KeyTeamResolver(ks store.ProxyKeyStore) TeamResolver {
	return func(r *http.Request) (string, error) {
		keyID := chi.URLParam(r, "id")
		if keyID == "" {
			return "", errTeamNotResolved
		}
		sc := auth.SessionFromContext(r.Context())
		if sc == nil || sc.TeamID == nil {
			return "", errTeamNotResolved
		}
		keys, err := ks.ListProxyKeysByTeam(r.Context(), *sc.TeamID)
		if err != nil {
			return "", errTeamNotResolved
		}
		for _, k := range keys {
			if k.ID == keyID {
				return *sc.TeamID, nil
			}
		}
		return "", errTeamNotResolved
	}
}

// redirectOrUnauthorized redirects page requests to /ui/login and returns 401
// for API requests.
func redirectOrUnauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/ui/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
		return
	}
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"log/slog"
	"net/http"
	"strings"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// UISessionMiddleware validates the llmp_session cookie against the database,
// loads the OIDC user (for oidc sessions), and injects a UISessionContext into
// the request context. Unauthenticated requests are redirected to /ui/login.
func UISessionMiddleware(sessions *auth.UISessionStore, st store.Store) func(http.Handler) http.Handler {
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

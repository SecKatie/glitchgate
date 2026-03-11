package web

import (
	"net/http"
	"strings"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
)

// SessionMiddleware checks for a valid session cookie or Authorization header.
// API requests get a 401; page requests are redirected to login.
func SessionMiddleware(sessions *auth.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""

			// Check cookie first.
			if c, err := r.Cookie("session"); err == nil {
				token = c.Value
			}

			// Fall back to Authorization header.
			if token == "" {
				if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
					token = bearer[7:]
				}
			}

			if token == "" || !sessions.Validate(token) {
				// API routes return 401 JSON.
				if strings.HasPrefix(r.URL.Path, "/ui/api/") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
					return
				}
				// Page routes redirect to login.
				http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

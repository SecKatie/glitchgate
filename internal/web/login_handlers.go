// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// LoginPage renders the login form.
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Redirect authenticated users directly to the main page.
	if c, err := r.Cookie("llmp_session"); err == nil && c.Value != "" {
		if sess, _ := h.sessions.Validate(r.Context(), c.Value); sess != nil {
			http.Redirect(w, r, "/ui/", http.StatusSeeOther)
			return
		}
	}

	oidcEnabled := h.oidc != nil && h.oidc.Enabled()
	showMasterKeyForm := !oidcEnabled || r.URL.Query().Get("master") == "1"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{
		"Error":             "",
		"OIDCEnabled":       oidcEnabled,
		"ShowMasterKeyForm": showMasterKeyForm,
	}); err != nil {
		slog.Error("render login page", "error", err)
	}
}

// LoginHandler processes the login form submission.
func (h *Handlers) LoginHandler(w http.ResponseWriter, r *http.Request) {
	// Limit request body to 1MB to prevent memory exhaustion.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	// Support both form and JSON.
	masterKey := r.FormValue("master_key")
	if masterKey == "" {
		var body struct {
			MasterKey string `json:"master_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			slog.Warn("decode login body", "error", err)
		}
		masterKey = body.MasterKey
	}

	if len(masterKey) == 0 || subtle.ConstantTimeCompare([]byte(masterKey), []byte(h.masterKey)) != 1 {
		if err := h.store.RecordAuditEvent(r.Context(), "master_key.login_failed", "", "", ""); err != nil {
			slog.Warn("record audit event", "error", err)
		}
		contentType := r.Header.Get("Content-Type")
		if contentType == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(`{"error":"Invalid master key"}`)); err != nil {
				slog.Error("write login error response", "error", err)
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Invalid master key"}); err != nil {
			slog.Error("render login page", "error", err)
		}
		return
	}

	sess, err := h.sessions.Create(r.Context(), "master_key", "")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "llmp_session",
		Value:    sess.Token,
		Path:     "/ui",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   28800,
	})

	if err := h.store.RecordAuditEvent(r.Context(), "master_key.login", "", "", "master_key"); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"session_token": sess.Token,
			"expires_at":    sess.ExpiresAt.Format(time.RFC3339),
		}); err != nil {
			slog.Error("write login response", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/ui/logs", http.StatusSeeOther)
}

// LogoutHandler invalidates the session and redirects to login.
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("llmp_session"); err == nil {
		if delErr := h.sessions.Delete(r.Context(), c.Value); delErr != nil {
			slog.Warn("delete session", "error", delErr)
		}
	}
	if err := h.store.RecordAuditEvent(r.Context(), "session.logout", "", "", sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}
	for _, name := range []string{"llmp_session", "session"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/ui",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

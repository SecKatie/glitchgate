// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/oidc"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// AuthFlowStore combines the store operations needed by the OIDC login flow.
type AuthFlowStore interface {
	store.OIDCStateStore
	store.OIDCUserStore
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
}

// AuthHandlers holds OIDC-specific HTTP handlers.
type AuthHandlers struct {
	store    AuthFlowStore
	sessions *auth.UISessionStore
	provider *oidc.Provider
}

// NewAuthHandlers creates OIDC auth handlers.
func NewAuthHandlers(st AuthFlowStore, sessions *auth.UISessionStore, provider *oidc.Provider) *AuthHandlers {
	return &AuthHandlers{
		store:    st,
		sessions: sessions,
		provider: provider,
	}
}

// OIDCStartHandler initiates the OIDC authorization code flow.
// GET /ui/auth/oidc
func (h *AuthHandlers) OIDCStartHandler(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.NotFound(w, r)
		return
	}

	state, err := oidc.GenerateState()
	if err != nil {
		slog.Error("OIDCStartHandler generate state", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	verifier, challenge, err := oidc.GeneratePKCEPair()
	if err != nil {
		slog.Error("OIDCStartHandler generate pkce", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	redirectTo := sanitizeRedirect(r.URL.Query().Get("redirect_to"))
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	if err := h.store.CreateOIDCState(r.Context(), state, verifier, redirectTo, expiresAt); err != nil {
		slog.Error("OIDCStartHandler store state", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, h.provider.AuthURL(state, challenge), http.StatusFound)
}

// OIDCCallbackHandler handles the IDP redirect after authentication.
// GET /ui/auth/callback
func (h *AuthHandlers) OIDCCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.NotFound(w, r)
		return
	}

	// IDP reported an error.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		slog.Warn("OIDC callback: IDP returned an error")
		if err := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", errParam); err != nil {
			slog.Warn("record audit event", "error", err)
		}
		http.Error(w, "Authentication error: "+errParam, http.StatusUnauthorized)
		return
	}

	// Validate and consume the state (one-time, atomic, TTL-checked).
	stateParam := r.URL.Query().Get("state")
	oidcState, err := h.store.ConsumeOIDCState(r.Context(), stateParam)
	if err != nil {
		slog.Error("OIDCCallbackHandler consume state", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if oidcState == nil {
		http.Error(w, "Invalid or expired login session. Please try again.", http.StatusBadRequest)
		return
	}

	// Exchange the code for tokens and extract claims.
	claims, err := h.provider.Exchange(r.Context(), r.URL.Query().Get("code"), oidcState.PKCEVerifier)
	if err != nil {
		slog.Error("OIDCCallbackHandler exchange", "error", err)
		if auditErr := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", "exchange error"); auditErr != nil {
			slog.Warn("record audit event", "error", auditErr)
		}
		http.Error(w, "Authentication failed. Please try again.", http.StatusUnauthorized)
		return
	}

	// Upsert the user (first user becomes global_admin).
	user, err := h.store.UpsertOIDCUser(r.Context(), claims.Subject, claims.Email, claims.DisplayName)
	if err != nil {
		slog.Error("OIDCCallbackHandler upsert user", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Deactivated users are rejected before a session is created.
	if !user.Active {
		if auditErr := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", "deactivated: "+user.Email); auditErr != nil {
			slog.Warn("record audit event", "error", auditErr)
		}
		http.Error(w, "Your account has been deactivated. Contact an administrator.", http.StatusForbidden)
		return
	}

	// Create a DB-backed session.
	sess, err := h.sessions.Create(r.Context(), "oidc", user.ID)
	if err != nil {
		slog.Error("OIDCCallbackHandler create session", "error", err)
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

	if err := h.store.RecordAuditEvent(r.Context(), "oidc.login", "", user.Email); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	dest := oidcState.RedirectTo
	if dest == "" || !isLocalPath(dest) {
		dest = "/ui/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// sanitizeRedirect returns the redirect target if it is a safe local path,
// otherwise returns an empty string so callers fall back to the default.
func sanitizeRedirect(dest string) string {
	if isLocalPath(dest) {
		return dest
	}
	return ""
}

// isLocalPath returns true if dest is a relative path that stays on the same
// origin: starts with exactly one slash, contains no backslashes, and has no
// authority component (// or scheme:).
func isLocalPath(dest string) bool {
	return strings.HasPrefix(dest, "/") &&
		!strings.HasPrefix(dest, "//") &&
		!strings.Contains(dest, "\\") &&
		!strings.Contains(dest, "://")
}

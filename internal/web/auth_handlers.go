// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"log"
	"net/http"
	"time"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
	"codeberg.org/kglitchy/llm-proxy/internal/oidc"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
)

// AuthHandlers holds OIDC-specific HTTP handlers.
type AuthHandlers struct {
	store    store.Store
	sessions *auth.UISessionStore
	provider *oidc.Provider
}

// NewAuthHandlers creates OIDC auth handlers.
func NewAuthHandlers(st store.Store, sessions *auth.UISessionStore, provider *oidc.Provider) *AuthHandlers {
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
		log.Printf("ERROR: OIDCStartHandler generate state: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	verifier, challenge, err := oidc.GeneratePKCEPair()
	if err != nil {
		log.Printf("ERROR: OIDCStartHandler generate pkce: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	redirectTo := r.URL.Query().Get("redirect_to")
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	if err := h.store.CreateOIDCState(r.Context(), state, verifier, redirectTo, expiresAt); err != nil {
		log.Printf("ERROR: OIDCStartHandler store state: %v", err)
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
		log.Printf("WARNING: OIDC callback: IDP returned an error (recorded to audit log)")
		if err := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", errParam); err != nil {
			log.Printf("WARNING: record audit event: %v", err)
		}
		http.Error(w, "Authentication error: "+errParam, http.StatusUnauthorized)
		return
	}

	// Validate and consume the state (one-time, atomic, TTL-checked).
	stateParam := r.URL.Query().Get("state")
	oidcState, err := h.store.ConsumeOIDCState(r.Context(), stateParam)
	if err != nil {
		log.Printf("ERROR: OIDCCallbackHandler consume state: %v", err)
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
		log.Printf("ERROR: OIDCCallbackHandler exchange: %v", err)
		if auditErr := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", "exchange error"); auditErr != nil {
			log.Printf("WARNING: record audit event: %v", auditErr)
		}
		http.Error(w, "Authentication failed. Please try again.", http.StatusUnauthorized)
		return
	}

	// Upsert the user (first user becomes global_admin).
	user, err := h.store.UpsertOIDCUser(r.Context(), claims.Subject, claims.Email, claims.DisplayName)
	if err != nil {
		log.Printf("ERROR: OIDCCallbackHandler upsert user: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Deactivated users are rejected before a session is created.
	if !user.Active {
		if auditErr := h.store.RecordAuditEvent(r.Context(), "oidc.login_failed", "", "deactivated: "+user.Email); auditErr != nil {
			log.Printf("WARNING: record audit event: %v", auditErr)
		}
		http.Error(w, "Your account has been deactivated. Contact an administrator.", http.StatusForbidden)
		return
	}

	// Create a DB-backed session.
	sess, err := h.sessions.Create(r.Context(), "oidc", user.ID)
	if err != nil {
		log.Printf("ERROR: OIDCCallbackHandler create session: %v", err)
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
		log.Printf("WARNING: record audit event: %v", err)
	}

	dest := oidcState.RedirectTo
	if dest == "" {
		dest = "/ui/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

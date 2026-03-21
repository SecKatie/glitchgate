// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// validateLabel checks that a key label is non-empty and within the max length.
func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("label is required")
	}
	if len(label) > 64 {
		return fmt.Errorf("label must be 64 characters or fewer")
	}
	return nil
}

// KeysPage renders the key management page.
func (h *Handlers) KeysPage(w http.ResponseWriter, r *http.Request) {
	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActiveTab": "keys",
		"Keys":      keys,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// KeysAPIHandler returns key list as HTMX fragment or JSON.
func (h *Handlers) KeysAPIHandler(w http.ResponseWriter, r *http.Request) {
	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"keys": keys}); err != nil {
		slog.Error("write keys JSON response", "error", err)
	}
}

// listKeysForSession returns the keys visible to the current session.
func (h *Handlers) listKeysForSession(r *http.Request) ([]store.ProxyKeySummary, error) {
	scope := visibleKeyScope(auth.SessionFromContext(r.Context()))
	switch scope.scopeType {
	case "all":
		return h.store.ListActiveProxyKeys(r.Context())
	case "team":
		return h.store.ListProxyKeysByTeam(r.Context(), scope.scopeTeamID)
	case "user":
		return h.store.ListProxyKeysByOwner(r.Context(), scope.scopeUserID)
	default:
		return []store.ProxyKeySummary{}, nil
	}
}

// canMutateKey returns true if the session is allowed to modify the key with the given prefix.
// Global admins and master-key sessions can modify any key.
// Other users can only modify keys returned by listKeysForSession.
func (h *Handlers) canMutateKey(r *http.Request, prefix string) (bool, error) {
	sc := auth.SessionFromContext(r.Context())
	if sc == nil || sc.IsMasterKey || sc.Role == "global_admin" {
		return true, nil
	}
	visible, err := h.listKeysForSession(r)
	if err != nil {
		return false, err
	}
	for _, k := range visible {
		if k.KeyPrefix == prefix {
			return true, nil
		}
	}
	return false, nil
}

// CreateKeyHandler creates a new proxy key.
func (h *Handlers) CreateKeyHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	label := r.FormValue("label")
	if err := validateLabel(label); err != nil {
		keys, _ := h.listKeysForSession(r)
		data := map[string]any{
			"ActiveTab":  "keys",
			"Keys":       keys,
			"LabelError": err.Error(),
			"LabelValue": label,
		}
		setNavData(data, auth.SessionFromContext(r.Context()))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
			slog.Error("render keys page with error", "error", err)
		}
		return
	}

	plaintext, hash, prefix, err := auth.GenerateKey()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	id := uuid.New().String()
	sc := auth.SessionFromContext(r.Context())
	if sc != nil && !sc.IsMasterKey && sc.User != nil {
		if err := h.store.CreateProxyKeyForUser(r.Context(), id, hash, prefix, label, sc.User.ID); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.store.CreateProxyKey(r.Context(), id, hash, prefix, label); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	sc2 := auth.SessionFromContext(r.Context())
	auditAction := "key_created"
	if sc2 != nil && !sc2.IsMasterKey && sc2.User != nil {
		auditAction = "key.created_for_user"
	}
	if err := h.store.RecordAuditEvent(r.Context(), auditAction, prefix, label, sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	// Apply optional key scoping.
	if allowedModels := r.FormValue("allowed_models"); allowedModels != "" {
		patterns := parseCommaSeparated(allowedModels)
		if len(patterns) > 0 {
			if err := h.store.SetKeyAllowedModels(r.Context(), id, patterns); err != nil {
				slog.Warn("set allowed models", "error", err)
			}
		}
	}
	if rateLimitStr := r.FormValue("rate_limit"); rateLimitStr != "" {
		if rateLimit, err := strconv.Atoi(rateLimitStr); err == nil && rateLimit > 0 {
			burst, _ := strconv.Atoi(r.FormValue("rate_burst"))
			if burst <= 0 {
				burst = rateLimit / 4
				if burst < 1 {
					burst = 1
				}
			}
			if err := h.store.SetKeyRateLimit(r.Context(), id, rateLimit, burst); err != nil {
				slog.Warn("set key rate limit", "error", err)
			}
		}
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActiveTab":     "keys",
		"Keys":          keys,
		"CreatedKey":    plaintext,
		"CreatedPrefix": prefix,
		"CreatedLabel":  label,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// UpdateKeyLabelHandler updates a key's label.
func (h *Handlers) UpdateKeyLabelHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	label := r.FormValue("label")
	if err := validateLabel(label); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	allowed, err := h.canMutateKey(r, prefix)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.UpdateKeyLabel(r.Context(), prefix, label); err != nil {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write update key response", "error", err)
	}
}

// RevokeKeyHandler revokes a proxy key.
func (h *Handlers) RevokeKeyHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	allowed, err := h.canMutateKey(r, prefix)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.RevokeProxyKey(r.Context(), prefix); err != nil {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "key.revoked", prefix, "", sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write revoke key response", "error", err)
	}
}

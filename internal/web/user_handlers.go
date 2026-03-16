// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// UserHandlers handles user management pages and API.
type UserHandlers struct {
	store     store.UserAdminStore
	sessions  *auth.UISessionStore
	templates *TemplateSet
}

// NewUserHandlers creates user management handlers.
func NewUserHandlers(st store.UserAdminStore, sessions *auth.UISessionStore, tmpl *TemplateSet) *UserHandlers {
	return &UserHandlers{store: st, sessions: sessions, templates: tmpl}
}

// userWithTeam is the JSON/template projection returned to callers.
type userWithTeam struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	Active      bool    `json:"active"`
	TeamID      *string `json:"team_id,omitempty"`
	TeamName    *string `json:"team_name,omitempty"`
	LastSeenAt  *string `json:"last_seen_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// UsersPage renders the user management page.
func (h *UserHandlers) UsersPage(w http.ResponseWriter, r *http.Request) {
	users, err := h.listUsersWithTeam(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"ActiveTab": "users",
		"Users":     users,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "users.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// UsersAPIHandler returns the user list as JSON.
func (h *UserHandlers) UsersAPIHandler(w http.ResponseWriter, r *http.Request) {
	users, err := h.listUsersWithTeam(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"users": users}); err != nil {
		slog.Error("write users JSON", "error", err)
	}
}

// listUsersWithTeam fetches the user admin projection from the store layer.
func (h *UserHandlers) listUsersWithTeam(r *http.Request) ([]userWithTeam, error) {
	users, err := h.store.ListUsersWithTeams(r.Context())
	if err != nil {
		return nil, err
	}

	result := make([]userWithTeam, 0, len(users))
	for _, u := range users {
		ut := userWithTeam{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			Active:      u.Active,
			CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if u.LastSeenAt != nil {
			s := u.LastSeenAt.Format("2006-01-02T15:04:05Z")
			ut.LastSeenAt = &s
		}
		ut.TeamID = u.TeamID
		ut.TeamName = u.TeamName
		result = append(result, ut)
	}
	return result, nil
}

// ChangeRoleHandler changes the role of a user.
// POST /ui/api/users/{id}/role
func (h *UserHandlers) ChangeRoleHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	role := r.FormValue("role")

	validRoles := map[string]bool{"global_admin": true, "team_admin": true, "member": true}
	if !validRoles[role] {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}

	// Guard: cannot demote the last global_admin.
	current, err := h.store.GetOIDCUserByID(r.Context(), id)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if current.Role == "global_admin" && role != "global_admin" {
		count, err := h.store.CountGlobalAdmins(r.Context())
		if err != nil || count <= 1 {
			http.Error(w, "Cannot demote the last global administrator", http.StatusConflict)
			return
		}
	}

	if err := h.store.UpdateOIDCUserRole(r.Context(), id, role); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "user.role_changed", "", id+" → "+role, sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write change role response", "error", err)
	}
}

// DeactivateUserHandler deactivates a user.
// POST /ui/api/users/{id}/deactivate
func (h *UserHandlers) DeactivateUserHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sc := auth.SessionFromContext(r.Context())

	// Team Admin scope check.
	if sc != nil && !sc.IsMasterKey && sc.Role == "team_admin" {
		tm, err := h.store.GetTeamMembership(r.Context(), id)
		if err != nil || tm == nil || sc.TeamID == nil || tm.TeamID != *sc.TeamID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	// Guard: cannot deactivate the last global_admin.
	target, err := h.store.GetOIDCUserByID(r.Context(), id)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if target.Role == "global_admin" {
		count, err := h.store.CountGlobalAdmins(r.Context())
		if err != nil || count <= 1 {
			http.Error(w, "Cannot deactivate the last global administrator", http.StatusConflict)
			return
		}
	}

	if err := h.store.SetOIDCUserActive(r.Context(), id, false); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Immediately invalidate all sessions for this user.
	if err := h.store.DeleteUISessionsByUserID(r.Context(), id); err != nil {
		slog.Warn("delete sessions for deactivated user", "error", err)
	}

	if err := h.store.RecordAuditEvent(r.Context(), "user.deactivated", "", id, sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write deactivate response", "error", err)
	}
}

// ReactivateUserHandler reactivates a deactivated user (Global Admin only).
// POST /ui/api/users/{id}/reactivate
func (h *UserHandlers) ReactivateUserHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.SetOIDCUserActive(r.Context(), id, true); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "user.reactivated", "", id, sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write reactivate response", "error", err)
	}
}

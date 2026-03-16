// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// TeamHandlers handles team management pages and API.
type TeamHandlers struct {
	store     store.TeamAdminStore
	sessions  *auth.UISessionStore
	templates *TemplateSet
}

// NewTeamHandlers creates team management handlers.
func NewTeamHandlers(st store.TeamAdminStore, sessions *auth.UISessionStore, tmpl *TemplateSet) *TeamHandlers {
	return &TeamHandlers{store: st, sessions: sessions, templates: tmpl}
}

// teamWithMembers is the JSON/template projection for a team.
type teamWithMembers struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
}

// TeamsPage renders the team management page.
func (h *TeamHandlers) TeamsPage(w http.ResponseWriter, r *http.Request) {
	sc := auth.SessionFromContext(r.Context())
	teams, err := h.listTeamsForSession(r, sc)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	allUsers, err := h.store.ListOIDCUsers(r.Context())
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	isGA := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin")

	data := map[string]any{
		"ActiveTab": "teams",
		"Teams":     teams,
		"AllUsers":  allUsers,
		"IsGA":      isGA,
	}
	setNavData(data, sc)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "teams.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// TeamsAPIHandler returns the team list as JSON.
func (h *TeamHandlers) TeamsAPIHandler(w http.ResponseWriter, r *http.Request) {
	sc := auth.SessionFromContext(r.Context())
	teams, err := h.listTeamsForSession(r, sc)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"teams": teams}); err != nil {
		slog.Error("write teams JSON", "error", err)
	}
}

// listTeamsForSession returns teams visible to the current session.
// GA sees all teams; TA sees only their own team.
func (h *TeamHandlers) listTeamsForSession(r *http.Request, sc *auth.UISessionContext) ([]teamWithMembers, error) {
	teams, err := h.store.ListTeamsWithMemberCounts(r.Context())
	if err != nil {
		return nil, err
	}

	if sc != nil && !sc.IsMasterKey && sc.Role == "team_admin" && sc.TeamID != nil {
		for _, team := range teams {
			if team.ID != *sc.TeamID {
				continue
			}
			return []teamWithMembers{{
				ID:          team.ID,
				Name:        team.Name,
				Description: team.Description,
				MemberCount: team.MemberCount,
				CreatedAt:   team.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}}, nil
		}
		return nil, nil
	}

	// GA / master key: all teams.
	result := make([]teamWithMembers, 0, len(teams))
	for _, t := range teams {
		result = append(result, teamWithMembers{
			ID:          t.ID,
			Name:        t.Name,
			Description: t.Description,
			MemberCount: t.MemberCount,
			CreatedAt:   t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	return result, nil
}

// CreateTeamHandler creates a new team.
// POST /ui/api/teams
func (h *TeamHandlers) CreateTeamHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	name := r.FormValue("name")
	description := r.FormValue("description")

	if name == "" {
		http.Error(w, "team name is required", http.StatusBadRequest)
		return
	}

	id := uuid.New().String()
	if err := h.store.CreateTeam(r.Context(), id, name, description); err != nil {
		// SQLite UNIQUE constraint on name.
		if isUniqueConstraintErr(err) {
			http.Error(w, "team name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "team.created", "", id+" "+name); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id}); err != nil {
		slog.Error("write create team response", "error", err)
	}
}

// AddTeamMemberHandler assigns a user to a team.
// POST /ui/api/teams/{id}/members
func (h *TeamHandlers) AddTeamMemberHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	teamID := chi.URLParam(r, "id")
	userID := r.FormValue("user_id")
	sc := auth.SessionFromContext(r.Context())

	if userID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	// TA scope check: can only add to their own team.
	if sc != nil && !sc.IsMasterKey && sc.Role == "team_admin" {
		if sc.TeamID == nil || *sc.TeamID != teamID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.store.AssignUserToTeam(r.Context(), userID, teamID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "team.member_added", "", teamID+" "+userID); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write add member response", "error", err)
	}
}

// DeleteTeamHandler deletes a team and removes all its member assignments.
// DELETE /ui/api/teams/{id}
func (h *TeamHandlers) DeleteTeamHandler(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")

	team, err := h.store.GetTeamByID(r.Context(), teamID)
	if err != nil || team == nil {
		http.Error(w, "Team not found", http.StatusNotFound)
		return
	}

	if err := h.store.DeleteTeam(r.Context(), teamID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "team.deleted", "", teamID+" "+team.Name); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write delete team response", "error", err)
	}
}

// RemoveTeamMemberHandler removes a user from a team.
// DELETE /ui/api/teams/{id}/members/{userID}
func (h *TeamHandlers) RemoveTeamMemberHandler(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	userID := chi.URLParam(r, "userID")
	sc := auth.SessionFromContext(r.Context())

	// TA scope check: can only remove from their own team.
	if sc != nil && !sc.IsMasterKey && sc.Role == "team_admin" {
		if sc.TeamID == nil || *sc.TeamID != teamID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	// Verify membership belongs to this team before removing.
	tm, err := h.store.GetTeamMembership(r.Context(), userID)
	if err != nil || tm == nil || tm.TeamID != teamID {
		http.Error(w, "User is not a member of this team", http.StatusNotFound)
		return
	}

	if err := h.store.RemoveUserFromTeam(r.Context(), userID); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "team.member_removed", "", teamID+" "+userID); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write remove member response", "error", err)
	}
}

// isUniqueConstraintErr returns true when err is a SQLite UNIQUE constraint violation.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return len(msg) >= 6 && (contains(msg, "UNIQUE constraint failed") || contains(msg, "unique constraint"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

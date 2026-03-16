// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage users",
}

var usersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all users",
	RunE:  runUsersList,
}

var usersRoleCmd = &cobra.Command{
	Use:   "role <user-id> <role>",
	Short: "Change a user's role (global_admin, team_admin, member)",
	Args:  cobra.ExactArgs(2),
	RunE:  runUsersRole,
}

var usersDeactivateCmd = &cobra.Command{
	Use:   "deactivate <user-id>",
	Short: "Deactivate a user account",
	Args:  cobra.ExactArgs(1),
	RunE:  runUsersDeactivate,
}

var usersReactivateCmd = &cobra.Command{
	Use:   "reactivate <user-id>",
	Short: "Reactivate a user account",
	Args:  cobra.ExactArgs(1),
	RunE:  runUsersReactivate,
}

var usersJSON bool

func init() {
	usersListCmd.Flags().BoolVar(&usersJSON, "json", false, "output as JSON")

	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersRoleCmd)
	usersCmd.AddCommand(usersDeactivateCmd)
	usersCmd.AddCommand(usersReactivateCmd)
	rootCmd.AddCommand(usersCmd)
}

type userRow struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	Active      bool    `json:"active"`
	TeamID      *string `json:"team_id,omitempty"`
	TeamName    *string `json:"team_name,omitempty"`
	LastSeenAt  string  `json:"last_seen_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

func runUsersList(_ *cobra.Command, _ []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	users, err := st.ListUsersWithTeams(ctx)
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	rows := make([]userRow, len(users))
	for i, u := range users {
		lastSeen := ""
		if u.LastSeenAt != nil {
			lastSeen = u.LastSeenAt.Format("2006-01-02 15:04")
		}
		rows[i] = userRow{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			Active:      u.Active,
			TeamID:      u.TeamID,
			TeamName:    u.TeamName,
			LastSeenAt:  lastSeen,
			CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04"),
		}
	}

	if usersJSON {
		return printJSON(rows)
	}

	if len(rows) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	tw := newTabWriter(os.Stdout)
	_, _ = fmt.Fprintf(tw, "ID\tEMAIL\tNAME\tROLE\tACTIVE\tTEAM\tLAST SEEN\n")
	for _, r := range rows {
		team := "—"
		if r.TeamName != nil {
			team = *r.TeamName
		}
		active := "yes"
		if !r.Active {
			active = "no"
		}
		lastSeen := "—"
		if r.LastSeenAt != "" {
			lastSeen = r.LastSeenAt
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			truncate(r.ID, 12), r.Email, truncate(r.DisplayName, 20),
			r.Role, active, team, lastSeen)
	}
	return tw.Flush()
}

var validRoles = map[string]bool{
	"global_admin": true,
	"team_admin":   true,
	"member":       true,
}

func runUsersRole(_ *cobra.Command, args []string) error {
	userID, role := args[0], args[1]

	if !validRoles[role] {
		return fmt.Errorf("invalid role %q (must be global_admin, team_admin, or member)", role)
	}

	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()

	// Guard: don't demote the last global admin.
	user, err := st.GetOIDCUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user %q not found", userID)
	}

	if user.Role == "global_admin" && role != "global_admin" {
		count, err := st.CountGlobalAdmins(ctx)
		if err != nil {
			return fmt.Errorf("counting admins: %w", err)
		}
		if count <= 1 {
			return fmt.Errorf("cannot demote the last global admin")
		}
	}

	if err := st.UpdateOIDCUserRole(ctx, userID, role); err != nil {
		return fmt.Errorf("updating role: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "user_role_changed", "", fmt.Sprintf("user=%s role=%s", userID, role), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Updated user %s role to %s\n", userID, role)
	return nil
}

func runUsersDeactivate(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	userID := args[0]

	user, err := st.GetOIDCUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user %q not found", userID)
	}

	// Guard: don't deactivate the last global admin.
	if user.Role == "global_admin" {
		count, err := st.CountGlobalAdmins(ctx)
		if err != nil {
			return fmt.Errorf("counting admins: %w", err)
		}
		if count <= 1 {
			return fmt.Errorf("cannot deactivate the last global admin")
		}
	}

	if err := st.SetOIDCUserActive(ctx, userID, false); err != nil {
		return fmt.Errorf("deactivating user: %w", err)
	}

	// Kill all active sessions.
	if err := st.DeleteUISessionsByUserID(ctx, userID); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: clearing sessions: %v\n", err)
	}

	if err := st.RecordAuditEvent(ctx, "user_deactivated", "", fmt.Sprintf("user=%s", userID), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Deactivated user %s\n", userID)
	return nil
}

func runUsersReactivate(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	userID := args[0]

	if err := st.SetOIDCUserActive(ctx, userID, true); err != nil {
		return fmt.Errorf("reactivating user: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "user_reactivated", "", fmt.Sprintf("user=%s", userID), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Reactivated user %s\n", userID)
	return nil
}

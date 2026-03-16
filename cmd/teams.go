// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var teamsCmd = &cobra.Command{
	Use:   "teams",
	Short: "Manage teams",
}

var teamsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all teams",
	RunE:  runTeamsList,
}

var teamsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new team",
	Args:  cobra.ExactArgs(1),
	RunE:  runTeamsCreate,
}

var teamsDeleteCmd = &cobra.Command{
	Use:   "delete <team-id>",
	Short: "Delete a team",
	Args:  cobra.ExactArgs(1),
	RunE:  runTeamsDelete,
}

var teamsAddMemberCmd = &cobra.Command{
	Use:   "add-member <team-id> <user-id>",
	Short: "Add a user to a team",
	Args:  cobra.ExactArgs(2),
	RunE:  runTeamsAddMember,
}

var teamsRemoveMemberCmd = &cobra.Command{
	Use:   "remove-member <team-id> <user-id>",
	Short: "Remove a user from a team",
	Args:  cobra.ExactArgs(2),
	RunE:  runTeamsRemoveMember,
}

var (
	teamsJSON        bool
	teamsDescription string
)

func init() {
	teamsListCmd.Flags().BoolVar(&teamsJSON, "json", false, "output as JSON")
	teamsCreateCmd.Flags().StringVar(&teamsDescription, "description", "", "team description")

	teamsCmd.AddCommand(teamsListCmd)
	teamsCmd.AddCommand(teamsCreateCmd)
	teamsCmd.AddCommand(teamsDeleteCmd)
	teamsCmd.AddCommand(teamsAddMemberCmd)
	teamsCmd.AddCommand(teamsRemoveMemberCmd)
	rootCmd.AddCommand(teamsCmd)
}

type teamRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
}

func runTeamsList(_ *cobra.Command, _ []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	teams, err := st.ListTeamsWithMemberCounts(ctx)
	if err != nil {
		return fmt.Errorf("listing teams: %w", err)
	}

	rows := make([]teamRow, len(teams))
	for i, t := range teams {
		rows[i] = teamRow{
			ID:          t.ID,
			Name:        t.Name,
			Description: t.Description,
			MemberCount: t.MemberCount,
			CreatedAt:   t.CreatedAt.Format("2006-01-02 15:04"),
		}
	}

	if teamsJSON {
		return printJSON(rows)
	}

	if len(rows) == 0 {
		fmt.Println("No teams found.")
		return nil
	}

	tw := newTabWriter(os.Stdout)
	_, _ = fmt.Fprintf(tw, "ID\tNAME\tMEMBERS\tDESCRIPTION\tCREATED\n")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			r.ID, r.Name, r.MemberCount, truncate(r.Description, 30), r.CreatedAt)
	}
	return tw.Flush()
}

func runTeamsCreate(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	id := uuid.New().String()
	name := args[0]

	if err := st.CreateTeam(ctx, id, name, teamsDescription); err != nil {
		return fmt.Errorf("creating team: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "team_created", "", fmt.Sprintf("name=%s id=%s", name, id), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Created team: %s (ID: %s)\n", name, id)
	return nil
}

func runTeamsDelete(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	teamID := args[0]

	if err := st.DeleteTeam(ctx, teamID); err != nil {
		return fmt.Errorf("deleting team: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "team_deleted", "", fmt.Sprintf("id=%s", teamID), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Deleted team: %s\n", teamID)
	return nil
}

func runTeamsAddMember(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	teamID, userID := args[0], args[1]

	if err := st.AssignUserToTeam(ctx, userID, teamID); err != nil {
		return fmt.Errorf("adding member: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "team_member_added", "", fmt.Sprintf("team=%s user=%s", teamID, userID), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Added user %s to team %s\n", userID, teamID)
	return nil
}

func runTeamsRemoveMember(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	teamID, userID := args[0], args[1]

	// Verify the user is actually on this team.
	membership, err := st.GetTeamMembership(ctx, userID)
	if err != nil {
		return fmt.Errorf("checking membership: %w", err)
	}
	if membership == nil || membership.TeamID != teamID {
		return fmt.Errorf("user %s is not a member of team %s", userID, teamID)
	}

	if err := st.RemoveUserFromTeam(ctx, userID); err != nil {
		return fmt.Errorf("removing member: %w", err)
	}

	if err := st.RecordAuditEvent(ctx, "team_member_removed", "", fmt.Sprintf("team=%s user=%s", teamID, userID), "cli"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Removed user %s from team %s\n", userID, teamID)
	return nil
}

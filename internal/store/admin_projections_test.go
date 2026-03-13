// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListUsersWithTeams(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	ctx := context.Background()

	alpha, err := st.UpsertOIDCUser(ctx, "sub-alpha", "alpha@example.com", "Alpha")
	require.NoError(t, err)
	beta, err := st.UpsertOIDCUser(ctx, "sub-beta", "beta@example.com", "Beta")
	require.NoError(t, err)

	require.NoError(t, st.CreateTeam(ctx, "team-1", "Platform", "Primary team"))
	require.NoError(t, st.AssignUserToTeam(ctx, beta.ID, "team-1"))

	users, err := st.ListUsersWithTeams(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)

	require.Equal(t, alpha.ID, users[0].ID)
	require.Nil(t, users[0].TeamID)
	require.Nil(t, users[0].TeamName)

	require.Equal(t, beta.ID, users[1].ID)
	require.NotNil(t, users[1].TeamID)
	require.Equal(t, "team-1", *users[1].TeamID)
	require.NotNil(t, users[1].TeamName)
	require.Equal(t, "Platform", *users[1].TeamName)
}

func TestListTeamsWithMemberCounts(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	ctx := context.Background()

	userA, err := st.UpsertOIDCUser(ctx, "sub-a", "a@example.com", "A")
	require.NoError(t, err)
	userB, err := st.UpsertOIDCUser(ctx, "sub-b", "b@example.com", "B")
	require.NoError(t, err)
	userC, err := st.UpsertOIDCUser(ctx, "sub-c", "c@example.com", "C")
	require.NoError(t, err)

	require.NoError(t, st.CreateTeam(ctx, "team-1", "Alpha", "Alpha team"))
	require.NoError(t, st.CreateTeam(ctx, "team-2", "Beta", "Beta team"))
	require.NoError(t, st.AssignUserToTeam(ctx, userA.ID, "team-1"))
	require.NoError(t, st.AssignUserToTeam(ctx, userB.ID, "team-1"))
	require.NoError(t, st.AssignUserToTeam(ctx, userC.ID, "team-2"))

	teams, err := st.ListTeamsWithMemberCounts(ctx)
	require.NoError(t, err)
	require.Len(t, teams, 2)

	require.Equal(t, "team-1", teams[0].ID)
	require.Equal(t, "Alpha", teams[0].Name)
	require.Equal(t, 2, teams[0].MemberCount)

	require.Equal(t, "team-2", teams[1].ID)
	require.Equal(t, "Beta", teams[1].Name)
	require.Equal(t, 1, teams[1].MemberCount)
}

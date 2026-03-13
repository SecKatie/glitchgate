// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestMigration016NormalizesOwnershipAndBudgets(t *testing.T) {
	st := newUnmigratedTestStore(t)
	ctx := context.Background()

	goose.SetBaseFS(migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.UpToContext(ctx, st.db, "migrations", 15))

	userCreatedAt := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	teamCreatedAt := time.Date(2026, 3, 2, 11, 0, 0, 0, time.UTC)
	keyCreatedAt := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)

	_, err := st.db.ExecContext(ctx, `INSERT INTO oidc_users (
		id, subject, email, display_name, role, active, created_at, budget_limit_usd, budget_period
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "sub-1", "one@example.com", "One", "member", 1, userCreatedAt, 25.0, "monthly",
	)
	require.NoError(t, err)

	_, err = st.db.ExecContext(ctx, `INSERT INTO teams (
		id, name, description, created_at, budget_limit_usd, budget_period
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"team-1", "Team One", "primary", teamCreatedAt, 100.0, "rolling_30d",
	)
	require.NoError(t, err)

	_, err = st.db.ExecContext(ctx, `INSERT INTO team_memberships (user_id, team_id, joined_at) VALUES (?, ?, ?)`,
		"user-1", "team-1", teamCreatedAt,
	)
	require.NoError(t, err)

	_, err = st.db.ExecContext(ctx, `INSERT INTO proxy_keys (
		id, key_hash, key_prefix, label, created_at, owner_user_id, budget_limit_usd, budget_period
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"pk-1", "hash-1", "llmp_sk_norm1", "normalized", keyCreatedAt, "user-1", 5.0, "lifetime",
	)
	require.NoError(t, err)

	_, err = st.db.ExecContext(ctx, `UPDATE global_config SET value = ? WHERE key = 'budget_limit_usd'`, "250.5")
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, `UPDATE global_config SET value = ? WHERE key = 'budget_period'`, "monthly")
	require.NoError(t, err)

	require.NoError(t, goose.UpContext(ctx, st.db, "migrations"))

	require.False(t, tableHasColumn(t, st, "proxy_keys", "owner_user_id"))
	require.False(t, tableHasColumn(t, st, "proxy_keys", "budget_limit_usd"))
	require.False(t, tableHasColumn(t, st, "oidc_users", "budget_limit_usd"))
	require.False(t, tableHasColumn(t, st, "teams", "budget_limit_usd"))

	users, err := st.ListOIDCUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.NotNil(t, users[0].Budget.LimitUSD)
	require.Equal(t, 25.0, *users[0].Budget.LimitUSD)
	require.NotNil(t, users[0].Budget.Period)
	require.Equal(t, "monthly", *users[0].Budget.Period)

	team, err := st.GetTeamByID(ctx, "team-1")
	require.NoError(t, err)
	require.NotNil(t, team)
	require.NotNil(t, team.Budget.LimitUSD)
	require.Equal(t, 100.0, *team.Budget.LimitUSD)
	require.NotNil(t, team.Budget.Period)
	require.Equal(t, "rolling_30d", *team.Budget.Period)

	keys, err := st.ListProxyKeysByOwner(ctx, "user-1")
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, "llmp_sk_norm1", keys[0].KeyPrefix)

	var globalLimit float64
	var globalPeriod string
	err = st.db.QueryRowContext(ctx, `SELECT limit_usd, period FROM global_budget_settings WHERE id = 1`).Scan(&globalLimit, &globalPeriod)
	require.NoError(t, err)
	require.Equal(t, 250.5, globalLimit)
	require.Equal(t, "monthly", globalPeriod)

	_, err = st.db.ExecContext(ctx, `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at) VALUES (?, ?, ?, ?, ?)`,
		"pk-2", "hash-2", "llmp_sk_norm1", "duplicate-prefix", time.Now().UTC(),
	)
	require.Error(t, err)
}

func newUnmigratedTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	st := newTestStoreWithoutMigrations(t)
	return st
}

func newTestStoreWithoutMigrations(t *testing.T) *SQLiteStore {
	t.Helper()

	dir := t.TempDir()
	dbPath := fmt.Sprintf("%s/test.db", dir)
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	return st
}

func tableHasColumn(t *testing.T, st *SQLiteStore, tableName, columnName string) bool {
	t.Helper()

	query := fmt.Sprintf("PRAGMA table_info(%s)", tableName)
	rows, err := st.db.Query(query)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue any
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk))
		if strings.EqualFold(name, columnName) {
			return true
		}
	}

	require.NoError(t, rows.Err())
	return false
}

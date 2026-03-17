package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestRecordAuditEventRepairsDriftedSequence(t *testing.T) {
	db, execer := newStubPostgresDB(t, []stubExecStep{
		{
			queryContains: "INSERT INTO audit_events",
			err: &pgconn.PgError{
				Code:           duplicateKeySQLState,
				ConstraintName: "audit_events_pkey",
			},
		},
		{
			queryContains: "pg_get_serial_sequence('audit_events', 'id')",
		},
		{
			queryContains: "INSERT INTO audit_events",
		},
	})

	st := &PostgreSQLStore{db: db}
	err := st.RecordAuditEvent(context.Background(), "oidc.login", "", "user@example.com", "user@example.com")
	require.NoError(t, err)
	require.Len(t, execer.executedQueries(), 3)
}

func TestRecordAuditEventDoesNotRepairUnrelatedErrors(t *testing.T) {
	expectedErr := errors.New("write failed")
	db, execer := newStubPostgresDB(t, []stubExecStep{
		{
			queryContains: "INSERT INTO audit_events",
			err:           expectedErr,
		},
	})

	st := &PostgreSQLStore{db: db}
	err := st.RecordAuditEvent(context.Background(), "oidc.login", "", "user@example.com", "user@example.com")
	require.ErrorIs(t, err, expectedErr)
	require.Len(t, execer.executedQueries(), 1)
}

func TestSyncAuditEventsSequenceEmptyTableSafe(t *testing.T) {
	db, execer := newStubPostgresDB(t, []stubExecStep{
		{
			queryContains: "COALESCE((SELECT MAX(id) FROM audit_events), 1)",
		},
	})

	err := syncAuditEventsSequence(context.Background(), db)
	require.NoError(t, err)
	require.Len(t, execer.executedQueries(), 1)
}

type stubExecStep struct {
	queryContains string
	err           error
}

type stubPostgresDriver struct {
	t       *testing.T
	mu      sync.Mutex
	steps   []stubExecStep
	queries []string
}

func newStubPostgresDB(t *testing.T, steps []stubExecStep) (*sql.DB, *stubPostgresDriver) {
	t.Helper()

	driverName := fmt.Sprintf("stub-postgres-%d", stubDriverID.Add(1))
	d := &stubPostgresDriver{t: t, steps: steps}
	sql.Register(driverName, d)

	db, err := sql.Open(driverName, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db, d
}

var stubDriverID atomic.Uint64

func (d *stubPostgresDriver) Open(string) (driver.Conn, error) {
	return &stubPostgresConn{driver: d}, nil
}

func (d *stubPostgresDriver) executedQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	queries := make([]string, len(d.queries))
	copy(queries, d.queries)
	return queries
}

type stubPostgresConn struct {
	driver *stubPostgresDriver
}

func (c *stubPostgresConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (c *stubPostgresConn) Close() error {
	return nil
}

func (c *stubPostgresConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}

func (c *stubPostgresConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.driver.mu.Lock()
	defer c.driver.mu.Unlock()

	c.driver.queries = append(c.driver.queries, query)
	stepIndex := len(c.driver.queries) - 1
	require.Less(c.driver.t, stepIndex, len(c.driver.steps))

	step := c.driver.steps[stepIndex]
	require.Contains(c.driver.t, query, step.queryContains)
	if step.err != nil {
		return nil, step.err
	}

	return stubResult(1), nil
}

type stubResult int64

func (r stubResult) LastInsertId() (int64, error) {
	return 0, errors.New("not implemented")
}

func (r stubResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

var (
	_ driver.Driver        = (*stubPostgresDriver)(nil)
	_ driver.ExecerContext = (*stubPostgresConn)(nil)
)

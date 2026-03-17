// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

const duplicateKeySQLState = "23505"

type postgresExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func isPostgresConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == duplicateKeySQLState && pgErr.ConstraintName == constraint
}

func syncAuditEventsSequence(ctx context.Context, db postgresExecer) error {
	const query = `
SELECT setval(
	pg_get_serial_sequence('audit_events', 'id'),
	COALESCE((SELECT MAX(id) FROM audit_events), 1),
	EXISTS (SELECT 1 FROM audit_events)
)`

	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("sync audit event sequence: %w", err)
	}

	return nil
}

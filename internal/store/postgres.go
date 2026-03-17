// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" driver for database/sql
	"github.com/pressly/goose/v3"
)

//go:embed migrations_pg/*.sql
var migrationsPG embed.FS

// PostgreSQLStore implements Store backed by a PostgreSQL database.
type PostgreSQLStore struct {
	db *sql.DB
}

// Compile-time check that PostgreSQLStore satisfies Store.
var _ Store = (*PostgreSQLStore)(nil)

// NewPostgreSQLStore opens (or creates) a connection to the PostgreSQL database
// identified by dsn and returns a ready-to-use store.
func NewPostgreSQLStore(dsn string) (*PostgreSQLStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgreSQLStore{db: db}, nil
}

// Migrate runs all pending goose migrations for PostgreSQL.
func (s *PostgreSQLStore) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrationsPG)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, s.db, "migrations_pg"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// Close closes the underlying database connection.
func (s *PostgreSQLStore) Close() error {
	return s.db.Close()
}

// rebindForPostgres converts SQLite-style ? placeholders to PostgreSQL-style
// positional parameters ($1, $2, ...). The args slice is unchanged; only the
// query string is rewritten. The conversion is sequential so the first ? becomes
// $1, the second $2, and so on — matching the order of values in args.
func rebindForPostgres(query string) string {
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			fmt.Fprintf(&b, "$%d", n)
			n++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

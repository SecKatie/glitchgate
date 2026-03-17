// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"

	"github.com/seckatie/glitchgate/internal/config"
)

// Open creates the appropriate Store implementation based on cfg.
// If database_url is configured a PostgreSQLStore is returned;
// otherwise a SQLiteStore backed by database_path is returned.
func Open(cfg *config.Config) (Store, error) {
	if cfg.DBBackend() == "postgres" {
		st, err := NewPostgreSQLStore(cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("opening postgres store: %w", err)
		}
		return st, nil
	}
	st, err := NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite store: %w", err)
	}
	return st, nil
}

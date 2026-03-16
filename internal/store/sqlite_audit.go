// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
)

// ListAuditEvents returns a paginated, filtered list of audit events ordered
// by created_at DESC. The second return value is the total count matching the
// filters (for pagination UI).
func (s *SQLiteStore) ListAuditEvents(ctx context.Context, params ListAuditParams) ([]AuditEvent, int64, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit < 1 {
		params.Limit = 50
	}

	var where []string
	var args []any

	if params.Action != "" {
		where = append(where, "action = ?")
		args = append(args, params.Action)
	}
	if params.From != "" {
		where = append(where, "created_at >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		where = append(where, "created_at <= ?")
		args = append(args, params.To)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count total matching rows.
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_events %s", whereClause)
	var total int64
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit events: %w", err)
	}

	// Fetch the page.
	offset := (params.Page - 1) * params.Limit
	dataQuery := fmt.Sprintf(
		"SELECT id, action, key_prefix, detail, COALESCE(actor_email, ''), created_at FROM audit_events %s ORDER BY created_at DESC LIMIT ? OFFSET ?",
		whereClause,
	)
	dataArgs := append(args, params.Limit, offset) //nolint:gocritic // intentional append to new slice

	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.Action, &e.KeyPrefix, &e.Detail, &e.ActorEmail, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan audit event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate audit events: %w", err)
	}

	return events, total, nil
}

// ListDistinctAuditActions returns the set of distinct action values present
// in the audit_events table, sorted alphabetically.
func (s *SQLiteStore) ListDistinctAuditActions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT action FROM audit_events ORDER BY action")
	if err != nil {
		return nil, fmt.Errorf("list distinct audit actions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("scan audit action: %w", err)
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

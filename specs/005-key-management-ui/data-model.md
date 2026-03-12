# Data Model: Key Management UI

**Feature**: 005-key-management-ui | **Date**: 2026-03-11

## Existing Entities (no changes)

### proxy_keys

Already exists via migration `001_create_proxy_keys.sql`. No schema changes needed.

| Column     | Type     | Constraints        | Notes                              |
|------------|----------|--------------------|------------------------------------|
| id         | TEXT     | PRIMARY KEY        | UUID                               |
| key_hash   | TEXT     | NOT NULL, UNIQUE   | bcrypt hash — never displayed      |
| key_prefix | TEXT     | NOT NULL           | e.g., "llmp_sk_ab12" (12 chars)    |
| label      | TEXT     | NOT NULL           | Human-readable, max 64 chars (app-enforced) |
| created_at | DATETIME | NOT NULL           | UTC timestamp                      |
| revoked_at | DATETIME |                    | NULL if active, set on revocation  |

**State transitions**: `active` (revoked_at IS NULL) → `revoked` (revoked_at IS NOT NULL). One-way, irreversible.

**Label rules**: Not unique. Editable. Non-empty. Max 64 characters (enforced at app layer, not DB).

## New Entities

### audit_events (migration: `005_create_audit_events.sql`)

Records administrative actions on proxy keys for security audit trail.

| Column     | Type     | Constraints | Notes                                          |
|------------|----------|-------------|-------------------------------------------------|
| id         | INTEGER  | PRIMARY KEY AUTOINCREMENT | Sequential event ID              |
| action     | TEXT     | NOT NULL    | One of: "key_created", "key_revoked"           |
| key_prefix | TEXT     | NOT NULL    | Prefix of the affected key                      |
| detail     | TEXT     |             | Optional context (e.g., label at creation time) |
| created_at | DATETIME | NOT NULL    | UTC timestamp of the event                      |

**Design notes**:
- No foreign key to `proxy_keys` — audit events must survive even if key records are ever hard-deleted in the future.
- `key_prefix` is denormalized intentionally for human-readable audit queries.
- No `actor` column needed — this is a single-admin system with master key auth. If multi-user is added later, an `actor` column can be added via migration.

## New Store Interface Methods

### UpdateKeyLabel

```
UpdateKeyLabel(ctx context.Context, prefix string, label string) error
```

- Updates `label` on the active key matching `prefix`
- Returns error if no active key matches (same pattern as RevokeProxyKey)
- Validates label non-empty and ≤64 chars at handler level

### RecordAuditEvent

```
RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
```

- Inserts a new row into `audit_events`
- Called after successful key creation or revocation
- Fire-and-forget semantics — audit failure logged but does not block the operation

## Go Type Additions

```go
// In internal/store/store.go

type AuditEvent struct {
    ID        int64
    Action    string
    KeyPrefix string
    Detail    string
    CreatedAt time.Time
}
```

## Relationships

```
proxy_keys 1───∞ request_logs    (existing FK: proxy_key_id)
proxy_keys 1───∞ audit_events    (logical, via key_prefix — no FK constraint)
```

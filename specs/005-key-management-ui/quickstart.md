# Quickstart: Key Management UI

**Feature**: 005-key-management-ui | **Date**: 2026-03-11

## Prerequisites

- Go 1.24+
- Running llm-proxy instance with a configured master key

## Development Workflow

```bash
# Build and run
make build
./llm-proxy serve --config config.yaml

# Run tests
make test

# Lint
make lint
```

## Testing Key Management

### Via CLI

```bash
# Create a key
./llm-proxy keys create --label "Test Key"

# List active keys
./llm-proxy keys list

# Update a key's label
./llm-proxy keys update llmp_sk_ab12 --label "Renamed Key"

# Revoke a key
./llm-proxy keys revoke llmp_sk_ab12
```

### Via Web UI

1. Navigate to `http://localhost:<port>/ui/login`
2. Authenticate with the master key
3. Click "Keys" in the navigation
4. Use the form to create keys, click labels to edit, click "Revoke" to delete

## Files Changed

| File | Change |
|------|--------|
| `cmd/keys.go` | Add `keysUpdateCmd` |
| `cmd/serve.go` | Register key management routes |
| `internal/store/store.go` | Add `UpdateKeyLabel`, `RecordAuditEvent` to interface |
| `internal/store/sqlite.go` | Implement new store methods |
| `internal/store/migrations/005_create_audit_events.sql` | New audit table |
| `internal/web/handlers.go` | Add key management handlers |
| `internal/web/templates/layout.html` | Add Keys nav link |
| `internal/web/templates/keys.html` | New keys page |
| `internal/web/templates/fragments/key_rows.html` | New HTMX fragment |

## Migration

The new migration (`005_create_audit_events.sql`) runs automatically on startup via `st.Migrate()`. No manual steps needed.

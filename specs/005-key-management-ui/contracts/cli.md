# CLI Contracts: Key Management

**Feature**: 005-key-management-ui | **Date**: 2026-03-11

## Existing Commands (unchanged)

### `llm-proxy keys create --label <label>`

Creates a new proxy key. Prints plaintext key, prefix, and label. Exits 0 on success.

### `llm-proxy keys list`

Lists active keys in tabular format: PREFIX, LABEL, CREATED. Exits 0.

### `llm-proxy keys revoke <prefix>`

Revokes a key by prefix. Prints confirmation. Exits 0 on success, 1 if key not found.

---

## New Command

### `llm-proxy keys update <prefix> --label <label>`

**Purpose**: Update the label of an active key.

**Arguments**:

| Argument | Type   | Position   | Required | Description                   |
|----------|--------|------------|----------|-------------------------------|
| prefix   | string | positional | yes      | Key prefix (e.g., "llmp_sk_ab12") |

**Flags**:

| Flag    | Type   | Required | Constraints     | Description            |
|---------|--------|----------|-----------------|------------------------|
| --label | string | yes      | 1–64 characters | New label for the key  |

**Output (success)**:
```
Updated key llmp_sk_ab12: label set to "New Label"
```

**Output (not found)**:
```
Error: no active key found with prefix "llmp_sk_xxxx"
```

**Exit codes**: 0 on success, 1 on error.

**Implementation**: Follows the same pattern as `keysRevokeCmd` — positional arg for prefix, `RunE` function, opens store via `openStore()`, calls `st.UpdateKeyLabel(ctx, prefix, label)`.

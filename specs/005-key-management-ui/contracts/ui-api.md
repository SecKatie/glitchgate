# UI API Contracts: Key Management

**Feature**: 005-key-management-ui | **Date**: 2026-03-11

All endpoints require session authentication (cookie or `Authorization: Bearer <session_token>`).
Unauthenticated requests to `/ui/api/*` return `401 {"error":"Unauthorized"}`.
Unauthenticated requests to `/ui/*` (non-API) redirect to `/ui/login`.

---

## GET /ui/keys

**Purpose**: Render the full keys management page.

**Response**: `200 text/html` — Full HTML page with layout, navigation (Keys tab active), key list table, and create form.

**Template data**:
```
{
  "ActiveTab": "keys",
  "Keys":      []ProxyKeySummary,  // active keys, ordered by created_at DESC
}
```

---

## GET /ui/api/keys

**Purpose**: Return key list as HTMX fragment or JSON.

**Response (HTMX)**: `200 text/html` — Renders `key_rows` fragment (table rows only).
**Response (JSON)**: `200 application/json`

```json
{
  "keys": [
    {
      "id": "uuid",
      "key_prefix": "llmp_sk_ab12",
      "label": "Production",
      "created_at": "2026-03-11T10:00:00Z"
    }
  ]
}
```

**HTMX detection**: `HX-Request: true` header.

---

## POST /ui/api/keys

**Purpose**: Create a new proxy key.

**Request**: `application/x-www-form-urlencoded`

| Field | Type   | Required | Constraints       |
|-------|--------|----------|-------------------|
| label | string | yes      | 1–64 characters   |

**Response (success)**: `200 text/html` — Renders keys page with plaintext key alert at top.

**Template data** (on success):
```
{
  "ActiveTab":    "keys",
  "Keys":         []ProxyKeySummary,
  "CreatedKey":   "llmp_sk_abcdef1234...",  // full plaintext, shown once
  "CreatedPrefix": "llmp_sk_ab12",
  "CreatedLabel": "Production",
}
```

**Response (validation error)**: `400 text/html` — Re-renders form with error message.

**Side effects**: Inserts into `proxy_keys` and `audit_events`.

---

## POST /ui/api/keys/{prefix}/update

**Purpose**: Update a key's label.

**Request**: `application/x-www-form-urlencoded`

| Field | Type   | Required | Constraints       |
|-------|--------|----------|-------------------|
| label | string | yes      | 1–64 characters   |

**Response (HTMX)**: `200 text/html` — Renders updated `key_rows` fragment.
**Response (JSON)**: `200 application/json` — `{"ok": true}`

**Response (not found)**: `404` — No active key with that prefix.
**Response (validation error)**: `400` — Empty or too-long label.

---

## POST /ui/api/keys/{prefix}/revoke

**Purpose**: Revoke (soft-delete) a key.

**Response (HTMX)**: `200 text/html` — Renders updated `key_rows` fragment (key removed).
**Response (JSON)**: `200 application/json` — `{"ok": true}`

**Response (not found)**: `404` — No active key with that prefix (idempotent — already revoked is treated as not found).

**Side effects**: Sets `revoked_at` on key, inserts into `audit_events`.

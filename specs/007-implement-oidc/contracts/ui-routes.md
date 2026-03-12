# UI Route Contracts: OIDC Authentication for User & Team Management

**Branch**: `007-implement-oidc` | **Date**: 2026-03-11

All routes are under the `/ui` prefix. Protected routes require a valid
`llmp_session` cookie. Authorization levels:
- **Any**: any valid session (OIDC or master key)
- **GA**: Global Admin or master key session
- **TA**: Team Admin (scoped to own team) or GA
- **Public**: no session required

---

## Authentication Routes

### `GET /ui/login`

**Auth**: Public

Returns the login page HTML.

| Condition | Behavior |
|---|---|
| OIDC configured, no `?master=1` | Shows OIDC "Sign In" button only |
| OIDC configured, `?master=1` | Shows both OIDC button and master key form |
| OIDC not configured | Shows master key form only |
| Valid session already exists | Redirects to `/ui/` |

**Query params**:
- `master=1` — reveal master key form (break-glass mode)
- `redirect` — post-login redirect destination (validated; must start with `/ui/`)

---

### `GET /ui/auth/oidc` (NEW)

**Auth**: Public

Starts the OIDC authorization code flow.

**Behavior**:
1. Generates `state` nonce and PKCE verifier; stores in `oidc_state` table (10 min TTL).
2. Builds the IDP authorization URL with `state`, `code_challenge` (S256), and configured scopes.
3. Redirects (302) to the IDP.

**Error cases**:
- OIDC not configured → 404.

---

### `GET /ui/auth/callback` (NEW)

**Auth**: Public (session is established here)

Handles the IDP redirect after authentication.

**Request query params**: `code`, `state`, `error` (optional).

**Behavior**:
1. If `error` is present → render error page with message; no session created.
2. Validate `state` exists in `oidc_state` table and is not expired → retrieve + delete row.
3. Exchange `code` for tokens using the stored PKCE verifier.
4. Verify ID token with `go-oidc` verifier.
5. Extract `sub`, `email`, `name` claims.
6. Upsert `oidc_users` (create if new, update email/name if returning).
7. First-ever OIDC user → role set to `global_admin`.
8. Create `ui_sessions` row; set `llmp_session` cookie.
9. Redirect to `redirect_to` from state (or `/ui/`).

**Error responses**:
- Invalid/expired state → 400 + error page.
- Token exchange failure → 400 + error page.
- ID token verification failure → 400 + error page.
- User is deactivated → 403 + error page.

---

### `POST /ui/api/login`

**Auth**: Public (existing; unchanged)

Master key form submission. Validates against configured `master_key`.
On success: creates `ui_sessions` row (type `master_key`), sets cookie.

---

### `POST /ui/api/logout`

**Auth**: Any (existing; behavior extended)

Deletes the `ui_sessions` row matching the current cookie token.
Clears `llmp_session` cookie. Redirects to `/ui/login`.

---

## User Management Routes (NEW)

### `GET /ui/users`

**Auth**: GA

Returns the users management page HTML.

---

### `GET /ui/api/users`

**Auth**: GA

Returns JSON list of all OIDC users.

**Response**:
```json
{
  "users": [
    {
      "id": "uuid",
      "email": "alice@example.com",
      "display_name": "Alice",
      "role": "global_admin",
      "active": true,
      "team_id": "uuid-or-null",
      "team_name": "Engineering",
      "last_seen_at": "2026-03-11T10:00:00Z",
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

---

### `POST /ui/api/users/{id}/role`

**Auth**: GA

Changes the role of the specified user.

**Request body**:
```json
{ "role": "team_admin" }
```

**Validation**:
- `role` must be one of `global_admin`, `team_admin`, `member`.
- Cannot demote the last `global_admin` → 409 + error message.
- Cannot change own role if last `global_admin` → 409 + error message.

**Response**: 200 on success; 409 with JSON error on last-admin guard.

---

### `POST /ui/api/users/{id}/deactivate`

**Auth**: GA (any user); TA (own team members only)

Deactivates the specified user. All their active sessions are invalidated.

**Validation**:
- Cannot deactivate the last `global_admin` → 409.
- TA cannot deactivate users outside their team → 403.

---

### `POST /ui/api/users/{id}/reactivate`

**Auth**: GA

Reactivates a previously deactivated user.

---

## Team Management Routes (NEW)

### `GET /ui/teams`

**Auth**: GA or TA

Returns the teams management page HTML.
- GA sees all teams and all members.
- TA sees only their own team and its members.

---

### `GET /ui/api/teams`

**Auth**: GA or TA

Returns JSON list of teams (scoped as above).

**Response**:
```json
{
  "teams": [
    {
      "id": "uuid",
      "name": "Engineering",
      "description": "Backend team",
      "member_count": 4,
      "created_at": "2026-01-01T00:00:00Z",
      "budget_limit_usd": null,
      "budget_period": null
    }
  ]
}
```

---

### `POST /ui/api/teams`

**Auth**: GA

Creates a new team.

**Request body**:
```json
{ "name": "Engineering", "description": "Backend team" }
```

**Validation**: `name` required, unique (409 if duplicate).

---

### `POST /ui/api/teams/{id}/members`

**Auth**: GA or TA (for own team)

Assigns a user to this team. Replaces any existing team assignment.

**Request body**:
```json
{ "user_id": "uuid" }
```

**Validation**:
- User must exist and be active.
- TA can only add users to their own team → 403 otherwise.

---

### `DELETE /ui/api/teams/{id}/members/{userID}`

**Auth**: GA or TA (for own team)

Removes a user from this team (makes them unassigned).

---

## Keys Routes (MODIFIED SCOPING)

### `GET /ui/api/keys`

**Auth**: Any (existing route; scoping logic added)

Returns keys scoped by role:
- GA / master key: all keys.
- TA: keys owned by all members of their team.
- Member: only their own keys.

**Response**: unchanged shape; filtered results.

---

### `POST /ui/api/keys`

**Auth**: Any (existing route; owner association added)

Creates a new proxy API key.
- OIDC session: key is associated with the authenticated user (`owner_user_id`).
- Master key session: key is created with `owner_user_id = NULL` (legacy/unowned).

**Request body**: unchanged (`{ "label": "My Key" }`).

---

### `POST /ui/api/keys/{prefix}/revoke`

**Auth**: Any (existing route; scope enforcement added)

Revokes a proxy key.
- GA / master key: can revoke any key.
- TA: can revoke keys owned by their team members.
- Member: can only revoke their own keys → 403 otherwise.

---

## Logs Routes (MODIFIED SCOPING)

### `GET /ui/api/logs`

**Auth**: Any (existing route; scoping added)

Returns logs scoped by role:
- GA / master key: all logs.
- TA: logs for all keys owned by their team members.
- Member: only logs for their own keys.

**Query params**: unchanged (`page`, `per_page`, `model`, `status`, `key_prefix`, `from`, `to`).

---

## Cost Routes (MODIFIED SCOPING)

### `GET /ui/api/costs`

**Auth**: Any (existing route; scoping added)

Cost summary scoped by role (same rules as logs). Members see their team's aggregate
cost summary (no per-user breakdown) in addition to their own key-level cost.

---

## Session Context (Internal)

All protected handlers receive a `UISessionContext` from middleware, injected into `context.Context`:

```go
type UISessionContext struct {
    SessionID   string
    SessionType string    // "oidc" | "master_key"
    IsMasterKey bool
    User        *store.OIDCUser // nil if master_key
    TeamID      *string         // nil if unassigned or GA/master_key
}
```

Helper: `web.SessionFromContext(ctx) *UISessionContext`

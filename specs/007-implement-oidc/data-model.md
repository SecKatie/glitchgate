# Data Model: OIDC Authentication for User & Team Management

**Branch**: `007-implement-oidc` | **Date**: 2026-03-11

---

## Existing Tables (unchanged schema)

| Table | Notes |
|---|---|
| `proxy_keys` | Extended — see Migration 011 |
| `request_logs` | Unchanged |
| `audit_events` | Extended to log OIDC/role/team events |

---

## New Tables

### `oidc_users`

Stores all OIDC-authenticated user accounts. Master key sessions are **not** stored here.

```sql
-- Migration 006
CREATE TABLE oidc_users (
    id           TEXT PRIMARY KEY,   -- UUID (generated server-side)
    subject      TEXT NOT NULL UNIQUE, -- IDP subject claim (stable across logins)
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL CHECK(role IN ('global_admin', 'team_admin', 'member')),
    active       INTEGER NOT NULL DEFAULT 1, -- 1 = active, 0 = deactivated
    last_seen_at DATETIME,
    created_at   DATETIME NOT NULL,
    -- Budget fields (schema-only; enforcement deferred to future feature)
    budget_limit_usd REAL,   -- NULL = no limit
    budget_period    TEXT    -- 'monthly' | 'rolling_30d' | 'lifetime' | NULL
);

CREATE INDEX idx_oidc_users_subject ON oidc_users(subject);
CREATE INDEX idx_oidc_users_email   ON oidc_users(email);
CREATE INDEX idx_oidc_users_role    ON oidc_users(role);
```

**Validation rules**:
- `subject` is immutable after creation; used as the primary matching key for returning users.
- `email` and `display_name` are updated on every successful login (may change at IDP).
- `role` transitions: `global_admin` ↔ `team_admin` ↔ `member` (any direction, subject to last-admin guard).
- `active = 0` immediately invalidates all `ui_sessions` for this user (enforced by session middleware).

**State transitions**:
```
[created: member]
    → role promoted by Global Admin → [global_admin | team_admin]
    → deactivated by Global Admin/Team Admin → [active=0]
    → reactivated by Global Admin → [active=1]
```

---

### `teams`

Named organizational groups. Each team can have a budget (deferred enforcement).

```sql
-- Migration 007
CREATE TABLE teams (
    id           TEXT PRIMARY KEY,   -- UUID
    name         TEXT NOT NULL UNIQUE,
    description  TEXT,
    created_at   DATETIME NOT NULL,
    -- Budget fields (schema-only; enforcement deferred)
    budget_limit_usd REAL,
    budget_period    TEXT
);
```

---

### `team_memberships`

1:1 user-to-team assignment. `user_id` is the PK, enforcing the one-team-per-user constraint at the DB level.

```sql
-- Migration 008
CREATE TABLE team_memberships (
    user_id   TEXT PRIMARY KEY REFERENCES oidc_users(id) ON DELETE CASCADE,
    team_id   TEXT NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
    joined_at DATETIME NOT NULL
);

CREATE INDEX idx_team_memberships_team_id ON team_memberships(team_id);
```

**Notes**:
- `ON DELETE RESTRICT` on `team_id` prevents deleting a team that still has members.
- Reassigning a user to a new team is an `INSERT OR REPLACE` (upsert by PK).
- `ON DELETE CASCADE` on `user_id` removes the membership row when a user is deleted (future).

---

### `ui_sessions`

Server-side session store for the web UI. Replaces the in-memory `auth.SessionStore`.

```sql
-- Migration 009
CREATE TABLE ui_sessions (
    id           TEXT PRIMARY KEY,   -- UUID
    token        TEXT NOT NULL UNIQUE, -- random 32-byte hex; placed in cookie
    session_type TEXT NOT NULL CHECK(session_type IN ('oidc', 'master_key')),
    user_id      TEXT REFERENCES oidc_users(id) ON DELETE CASCADE,
    -- NULL for master_key sessions
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL
);

CREATE INDEX idx_ui_sessions_token      ON ui_sessions(token);
CREATE INDEX idx_ui_sessions_expires_at ON ui_sessions(expires_at);
CREATE INDEX idx_ui_sessions_user_id    ON ui_sessions(user_id);
```

**Lifecycle**:
- Created on successful login (OIDC callback or master key POST).
- Validated on every protected UI request (single indexed read by `token`).
- Deleted on logout, user deactivation, or expiry.
- Cleanup goroutine removes `WHERE expires_at < now()` at startup and every hour.

---

### `oidc_state`

Short-lived table storing PKCE verifier + state nonce during the OIDC authorization code flow.
Entries are single-use (deleted immediately after the callback consumes them).

```sql
-- Migration 010
CREATE TABLE oidc_state (
    state          TEXT PRIMARY KEY,   -- random nonce sent to IDP
    pkce_verifier  TEXT NOT NULL,      -- PKCE code_verifier (S256)
    redirect_to    TEXT,               -- post-login destination URL
    created_at     DATETIME NOT NULL,
    expires_at     DATETIME NOT NULL   -- 10 minutes from creation
);

CREATE INDEX idx_oidc_state_expires_at ON oidc_state(expires_at);
```

---

### Migrations to Existing Tables

#### Migration 011 — Add `owner_user_id` to `proxy_keys`

```sql
ALTER TABLE proxy_keys
    ADD COLUMN owner_user_id TEXT REFERENCES oidc_users(id);

CREATE INDEX idx_proxy_keys_owner_user_id ON proxy_keys(owner_user_id);
```

**Notes**:
- Nullable; legacy keys (pre-OIDC) have `owner_user_id = NULL`.
- New keys created by an OIDC session are linked to the authenticated user's `id`.
- New keys created by a master key session have `owner_user_id = NULL` (treated as legacy).

#### Migration 012 — Add budget fields to `proxy_keys`

```sql
ALTER TABLE proxy_keys ADD COLUMN budget_limit_usd REAL;
ALTER TABLE proxy_keys ADD COLUMN budget_period    TEXT;
```

#### Migration 013 — `global_config` table (org-level budget)

```sql
CREATE TABLE global_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Seed: org-level budget placeholders (NULL values = no limit)
INSERT INTO global_config (key, value) VALUES ('budget_limit_usd', 'null');
INSERT INTO global_config (key, value) VALUES ('budget_period',    'null');
```

---

## Entity Relationship Diagram (text)

```
global_config (1 row per config key)

oidc_users
  ├── id (PK)
  ├── subject (UNIQUE — IDP stable ID)
  ├── role: global_admin | team_admin | member
  ├── active: bool
  └── budget_limit_usd / budget_period (nullable)

proxy_keys
  ├── id (PK)
  ├── owner_user_id → oidc_users.id (nullable)
  ├── budget_limit_usd / budget_period (nullable)
  └── ... (existing fields)

request_logs
  └── proxy_key_id → proxy_keys.id

teams
  ├── id (PK)
  ├── name (UNIQUE)
  └── budget_limit_usd / budget_period (nullable)

team_memberships
  ├── user_id → oidc_users.id  [PK — enforces 1:1]
  └── team_id → teams.id

ui_sessions
  ├── token (UNIQUE — in cookie)
  ├── session_type: oidc | master_key
  └── user_id → oidc_users.id (nullable for master_key)

oidc_state  (ephemeral, 10-min TTL)
  ├── state (PK — nonce)
  └── pkce_verifier
```

---

## Store Interface Extensions

New methods to add to `internal/store/store.go`:

```go
// OIDC Users
UpsertOIDCUser(ctx, subject, email, displayName string) (*OIDCUser, error)
GetOIDCUserByID(ctx, id string) (*OIDCUser, error)
GetOIDCUserBySubject(ctx, subject string) (*OIDCUser, error)
ListOIDCUsers(ctx) ([]OIDCUser, error)
CountGlobalAdmins(ctx) (int64, error)
UpdateOIDCUserRole(ctx, id, role string) error
SetOIDCUserActive(ctx, id string, active bool) error
UpdateOIDCUserLastSeen(ctx, id string) error

// Teams
CreateTeam(ctx, id, name, description string) error
ListTeams(ctx) ([]Team, error)
GetTeamByID(ctx, id string) (*Team, error)

// Team Memberships
AssignUserToTeam(ctx, userID, teamID string) error
RemoveUserFromTeam(ctx, userID string) error
GetTeamMembership(ctx, userID string) (*TeamMembership, error)
ListTeamMembers(ctx, teamID string) ([]OIDCUser, error)

// UI Sessions
CreateUISession(ctx, id, token, sessionType, userID string, expiresAt time.Time) error
GetUISessionByToken(ctx, token string) (*UISession, error)
DeleteUISession(ctx, token string) error
DeleteUISessionsByUserID(ctx, userID string) error
CleanupExpiredSessions(ctx) error

// OIDC State
CreateOIDCState(ctx, state, pkceVerifier, redirectTo string, expiresAt time.Time) error
ConsumeOIDCState(ctx, state string) (*OIDCState, error)  // deletes on read
CleanupExpiredOIDCState(ctx) error

// Log scoping (extend ListRequestLogs params)
// ScopeUserID and ScopeTeamID added to existing ListLogsParams struct
// ScopeType: "all" | "team" | "user"

// Key scoping
ListProxyKeysByOwner(ctx, ownerUserID string) ([]ProxyKeySummary, error)
ListProxyKeysByTeam(ctx, teamID string) ([]ProxyKeySummary, error)
CreateProxyKeyForUser(ctx, id, keyHash, keyPrefix, label, ownerUserID string) error
```

---

## New Internal Types (`internal/store/store.go`)

```go
type OIDCUser struct {
    ID             string
    Subject        string
    Email          string
    DisplayName    string
    Role           string    // "global_admin" | "team_admin" | "member"
    Active         bool
    LastSeenAt     *time.Time
    CreatedAt      time.Time
    BudgetLimitUSD *float64
    BudgetPeriod   *string
}

type Team struct {
    ID             string
    Name           string
    Description    string
    CreatedAt      time.Time
    BudgetLimitUSD *float64
    BudgetPeriod   *string
}

type TeamMembership struct {
    UserID   string
    TeamID   string
    JoinedAt time.Time
}

type UISession struct {
    ID          string
    Token       string
    SessionType string // "oidc" | "master_key"
    UserID      *string
    CreatedAt   time.Time
    ExpiresAt   time.Time
}

type OIDCState struct {
    State        string
    PKCEVerifier string
    RedirectTo   string
    CreatedAt    time.Time
    ExpiresAt    time.Time
}
```

# Feature Specification: OIDC Authentication for User & Team Management

**Feature Branch**: `007-implement-oidc`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "let's add user and team management by first implementing OIDC."

## Context

Today the web UI has a single authentication mechanism: a shared master key configured by the operator. Anyone who knows the master key has full access to the entire UI. There are no user accounts, no roles, and no concept of teams.

This feature adds OIDC as a second authentication path and introduces a persistent user model with three roles (Global Admin, Team Admin, Member) and team scoping. Master key login is **not removed** — it is retained as a **break-glass emergency mechanism** for recovery scenarios (e.g., OIDC provider outage, all Global Admins locked out). It is **not intended for day-to-day use** and is deliberately hidden from the standard login page; operators must navigate to a special URL query parameter to reveal it.

The role hierarchy is:
- **Global Admin**: full, unscoped access to all logs, keys, teams, and users. Can create teams, assign roles, and manage all users.
- **Team Admin**: manages their own team — can add/remove members, view all logs for their team, and set budgets for team members. Scoped to their team for data access.
- **Member**: sees only their own logs and keys. Can view the team-level cost aggregate (how much of the team budget has been used) but cannot see other members' individual logs.

The system is also designed to support a future hierarchical budget system (org → team → user → key). The data model must accommodate budget limits at each scope level even if enforcement is not implemented in this iteration.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - OIDC User Signs In Alongside Existing Master Key Login (Priority: P1)

When OIDC is configured, the standard login page shows **only** the OIDC "Sign In with [Provider]" button. The master key form is hidden by default and only appears when the operator navigates to the login page with a specific URL query parameter (e.g., `/login?master=1`). This makes the master key a deliberate, out-of-band break-glass action rather than a routine login option. When OIDC is not configured, the master key form is shown normally (it is the only login method available).

**Why this priority**: This is the entry point for all OIDC-based identity. Everything else (roles, teams, user management) depends on OIDC users existing. It also establishes the non-breaking constraint: master key login must not regress.

**Independent Test**: Can be fully tested by configuring a test OIDC provider, verifying the master key login still works, and separately verifying an OIDC sign-in flow results in a named session.

**Acceptance Scenarios**:

1. **Given** OIDC is configured, **When** an unauthenticated user visits the standard login page, **Then** they see only the OIDC "Sign In" button; the master key form is not visible.
2. **Given** OIDC is configured, **When** an operator navigates to `/login?master=1`, **Then** the master key form is revealed alongside (or instead of) the OIDC button.
3. **Given** OIDC is configured, **When** a user completes the OIDC flow, **Then** they are returned to the proxy UI with their name/email visible in the navigation.
4. **Given** OIDC is configured and the operator navigates to `/login?master=1`, **When** a user submits a valid master key, **Then** they are logged in with full Global Admin access.
5. **Given** a valid session exists, **When** the user visits the UI, **Then** they are not required to re-authenticate.
6. **Given** a session has expired, **When** the user visits the UI, **Then** they are redirected to the login page.
7. **Given** OIDC is not configured, **When** a user visits the login page, **Then** they see only the master key form (OIDC button never appears) and behavior is unchanged from today.
8. **Given** the identity provider rejects authentication, **When** the callback is received, **Then** the user sees a clear error and is not granted a session.

---

### User Story 2 - First OIDC User Becomes Global Admin (Priority: P2)

The first person to sign in via OIDC is automatically granted the Global Admin role. Subsequent OIDC sign-ins create Member accounts. Master key sessions are always treated as Global Admin-equivalent regardless of the user table.

**Why this priority**: Without a bootstrap mechanism there is no way to manage users after OIDC is enabled. It also clarifies what master key sessions can and cannot do in the new model.

**Independent Test**: Can be tested by signing in via OIDC with no existing users, verifying Global Admin access, then signing in with a second OIDC account and verifying Member access.

**Acceptance Scenarios**:

1. **Given** no OIDC users exist, **When** the first user completes OIDC sign-in, **Then** they are created with the Global Admin role.
2. **Given** at least one OIDC user exists, **When** a new user completes OIDC sign-in, **Then** they are created with the Member role.
3. **Given** a user returns and completes OIDC sign-in again, **Then** their existing account is updated (not duplicated) and their role is preserved.
4. **Given** a master key session, **When** the user accesses any UI page, **Then** they have full Global Admin-level access to all data, regardless of whether any OIDC users exist.

---

### User Story 3 - Global Admin Manages All Users and Roles (Priority: P3)

A Global Admin (signed in via either OIDC or master key) can view all OIDC users, change their roles (Global Admin / Team Admin / Member), and deactivate them. Deactivation immediately revokes access for that user's sessions. A Team Admin can also manage the users within their own team.

**Why this priority**: User management enables scoped access and delegation. The Team Admin role in particular enables self-service team management without requiring Global Admin involvement for every membership change. Depends on P1 and P2 being complete.

**Independent Test**: Can be tested by signing in as Global Admin, promoting a user to Team Admin, having the Team Admin add a Member to their team, and verifying the Member's scoped access.

**Acceptance Scenarios**:

1. **Given** a Global Admin is authenticated, **When** they visit the Users section, **Then** they see all OIDC users with name, email, role, team assignment, and last-seen date.
2. **Given** a Global Admin promotes a Member to Global Admin, **Then** that user gains Global Admin privileges on their next request.
3. **Given** a Global Admin promotes a Member to Team Admin, **Then** that user gains Team Admin privileges for their assigned team.
4. **Given** a Global Admin demotes any user to Member, **Then** that user loses elevated privileges on their next request.
5. **Given** a Global Admin deactivates a user, **When** that user makes any UI request, **Then** their session is rejected and they are signed out.
6. **Given** the last Global Admin attempts to demote or deactivate themselves, **Then** the system prevents it and shows a descriptive error.
7. **Given** a Team Admin is authenticated, **When** they visit their team's management page, **Then** they see all users in their team with name, email, role, and last-seen date.
8. **Given** a Team Admin adds an unassigned Member to their team, **Then** that user's team membership is updated and they gain visibility of the team's cost aggregate.
9. **Given** a Team Admin removes a Member from their team, **Then** that user becomes unassigned and loses team cost visibility.

---

### User Story 4 - Team Creation, Key Scoping, and Cost Visibility (Priority: P4)

A Global Admin creates teams and assigns users to them. Each user owns their own proxy API keys. Members see only their own logs and keys but can view the team-level cost aggregate (total spend and remaining budget for their team). Team Admins see all logs and keys for every member of their team. Global Admins and master key sessions always see everything.

**Why this priority**: Team scoping is the organizational value-add, but requires P1–P3 to be complete. The cost visibility model (member sees team aggregate, not individual peers' logs) protects privacy while enabling budget awareness.

**Independent Test**: Can be tested by creating a team with two members, verifying each member sees only their own logs, verifying both see the shared team cost aggregate, and verifying the Team Admin sees all logs for the team.

**Acceptance Scenarios**:

1. **Given** a Global Admin, **When** they create a team with a name and description, **Then** the team appears in the teams list.
2. **Given** a team exists, **When** a Global Admin or Team Admin assigns a user to it, **Then** that user's membership is updated and they gain access to the team's cost aggregate.
3. **Given** a member is assigned to Team A, **When** they view logs, **Then** they see only their own request logs (not other members' logs).
4. **Given** a member is assigned to Team A, **When** they view the team cost summary, **Then** they see the team's aggregate spend and remaining budget (when budget is configured).
5. **Given** a member is assigned to Team A, **When** they view keys, **Then** they see only their own keys.
6. **Given** a Team Admin is authenticated for Team A, **When** they view logs or keys, **Then** they see all Team A members' logs and keys.
7. **Given** a Global Admin or master key session, **When** they view logs or keys, **Then** they see all users' data across all teams.
8. **Given** a user has no team assignment, **When** they view logs or keys, **Then** they see only their own data and see no team cost summary; an unassigned notice is displayed.

---

### Edge Cases

- What happens when OIDC is misconfigured at startup? The OIDC sign-in button is not rendered; the master key form still works; an error is logged at startup.
- What happens when the identity provider is temporarily unavailable during sign-in? The user sees a friendly error and can retry or use the master key.
- What happens when a deactivated user still has an active session? Their next request is rejected and their session is invalidated.
- What happens when the last Global Admin is demoted or deactivated? The system prevents it. The master key break-glass login (via `/login?master=1`) remains available as a recovery path even if all OIDC Global Admins are somehow lost.
- What happens when a user's email changes at the identity provider? The account is matched by stable subject ID; email is updated, account is not duplicated.
- What happens to proxy keys that existed before OIDC users were introduced? They are legacy unmanaged keys, visible only to Global Admins and master key sessions. They are not automatically assigned to any OIDC user.
- What happens when OIDC is disabled after users and teams already exist? The user and team data is preserved. Only master key login is available until OIDC is re-enabled.
- What happens when a Team Admin is removed from their team or demoted to Member? They lose Team Admin privileges immediately; their sessions are re-evaluated on the next request.
- What happens when a member with no team assignment views the cost summary? They see no team cost data; only their own key-level usage is visible.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST support authentication via a single configurable OIDC provider using the authorization code flow with mandatory PKCE (`S256` challenge method).
- **FR-002**: The master key login form is a **break-glass emergency mechanism** — not for routine use. When OIDC is configured, the master key form MUST be hidden on the standard login page and only revealed when the operator accesses `/login?master=1`. When OIDC is not configured, the master key form is shown normally as the sole login method.
- **FR-003**: System MUST show an OIDC sign-in button on the standard login page only when OIDC is configured. When OIDC is not configured, no OIDC button is shown.
- **FR-004**: System MUST complete the OIDC callback, validate the ID token, and establish a named session for the authenticated user.
- **FR-005**: System MUST automatically assign the Global Admin role to the first OIDC user when no OIDC users exist.
- **FR-006**: System MUST assign the Member role by default to all subsequent new OIDC users.
- **FR-007**: System MUST display the authenticated OIDC user's name and email in the UI navigation; master key sessions may display a generic "Admin" indicator.
- **FR-008**: System MUST match returning OIDC users by stable subject identifier, updating their email/name if changed at the provider.
- **FR-009**: System MUST grant master key sessions full Global Admin-level access to all data regardless of team assignments or user table state.
- **FR-010**: System MUST allow Global Admins to view all OIDC users with their roles, team assignments, and last-seen timestamps. Team Admins may view users within their own team only.
- **FR-011**: System MUST allow Global Admins to change any OIDC user's role (Global Admin / Team Admin / Member). Team Admins may promote Members within their team to Team Admin (of the same team) or demote Team Admins to Member.
- **FR-012**: System MUST prevent the last OIDC Global Admin from being demoted or deactivated.
- **FR-013**: System MUST allow Global Admins to deactivate any OIDC user, immediately rejecting that user's active sessions. Team Admins may deactivate Members within their own team.
- **FR-014**: System MUST allow Global Admins to create and name teams.
- **FR-015**: System MUST allow each OIDC user to create, name, and delete their own proxy API keys (one or more). Keys are user-owned; no shared keys exist. Global Admins can view and revoke any user's keys; Team Admins can view and revoke keys for members of their team.
- **FR-016**: System MUST allow Global Admins to assign OIDC users to a single team. Team Admins may add unassigned users to their team or remove users from their team. A user may only belong to one team at a time; reassigning replaces the previous membership.
- **FR-017**: System MUST scope Member users' log views to their own request logs only. Members MUST NOT see other team members' individual log entries.
- **FR-018**: System MUST scope Member users' key views to their own keys only.
- **FR-019**: System MUST allow Member users to view a team-level cost aggregate (total spend and remaining budget for their team) without exposing individual peer log details.
- **FR-020**: System MUST grant Team Admin users full visibility of all logs and keys for every member of their team.
- **FR-021**: System MUST grant Global Admin users and master key sessions unscoped access to all logs, keys, and teams.
- **FR-022**: System MUST provide a sign-out mechanism that terminates the current session (OIDC or master key).
- **FR-023**: System MUST log all authentication and authorization events (sign-in, sign-out, failed login, role changes, team assignments) for audit purposes.
- **FR-024**: System MUST leave programmatic proxy API key access unaffected by any OIDC or team configuration.
- **FR-025** *(data model requirement — enforcement deferred)*: The data schema MUST include budget limit fields at org (global config), team, user, and key scope levels to support a future hierarchical budget enforcement system. Budget limits are nullable/unset by default; no enforcement logic is required in this iteration.

### Key Entities

- **User**: An OIDC-authenticated person. Has a stable identity provider subject ID, email, display name, role (Global Admin / Team Admin / Member), active/inactive status, last-seen timestamp, and one or more user-owned proxy API keys. Master key sessions are not stored as User records.
- **Team**: A named organizational group. Has a name, description, creation date, and an optional budget limit field (nullable; enforcement deferred to future budget feature).
- **Team Membership**: The association between a User and a Team. A user belongs to exactly one team at a time, or is unassigned. Multi-team membership is not supported; all usage and log attribution is tied to the user's single team.
- **Proxy API Key**: A named credential owned by a single User. A user may have multiple keys for their own usage tracking. Has a name, hashed secret, created-at timestamp, and an optional budget limit field (nullable; enforcement deferred).
- **OIDC Configuration**: Operator-supplied settings for identity provider integration. Stored in configuration, not the database. The OIDC flow is implemented using `golang.org/x/oauth2` (authorization code flow + PKCE) and `github.com/coreos/go-oidc/v3` (ID token validation: `iss`, `aud`, `exp`, nonce claims).
- **Session**: Associates an authenticated browser session with a User (OIDC) or admin-equivalent (master key). Has an expiry time of 8 hours (default; not operator-configurable in this iteration). Sessions are stored server-side in SQLite; the browser holds only a cryptographically random token in an HttpOnly, Secure, SameSite=Lax cookie. Session lookup on each request is a single indexed DB read by token. Immediate revocation (deactivation, sign-out) is achieved by deleting the session row.
- **Budget** *(planned — schema only in this iteration)*: A spending limit that can be scoped to org (global config), team, user, or key. Fields: `scope_type`, `scope_id`, `limit_usd`, `period` (monthly/rolling/lifetime). No enforcement logic in this iteration; fields are nullable and must not block existing functionality.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with no prior account can complete the full OIDC sign-in flow in under 30 seconds, excluding identity provider latency.
- **SC-002**: Master key login succeeds in all configurations (OIDC enabled, disabled, or misconfigured) when accessed via `/login?master=1`. The standard login page never exposes the master key form when OIDC is configured.
- **SC-003**: After deactivation, a user's access is revoked within one request—no grace period.
- **SC-004**: A member user sees only their own request logs; zero cross-user log leakage occurs under any tested scenario. Team cost aggregate is visible to members but contains no individual peer log detail.
- **SC-005**: The system supports at least 50 concurrent authenticated sessions without performance degradation.
- **SC-006**: All authentication and authorization events are recorded and retrievable by any Global Admin or master key session.
- **SC-007**: An operator can enable OIDC with only configuration file changes—no code modifications or manual database edits required.
- **SC-008**: The database schema includes budget limit fields at org, team, user, and key scope levels, verified by migration tests, without impacting existing request logging behavior.

## Clarifications

### Session 2026-03-11

- Q: How should authenticated sessions be stored? → A: Server-side sessions in SQLite; browser holds only a random signed token in an HttpOnly cookie.
- Q: Which Go library should implement the OIDC authorization code flow? → A: `golang.org/x/oauth2` + `github.com/coreos/go-oidc/v3` (ID token validation via go-oidc verifier).
- Q: What should the default session expiry duration be? → A: 8 hours.
- Q: Should a user be able to belong to multiple teams? → A: No — one user belongs to exactly one team (or is unassigned). Multi-team membership is out of scope; it creates ambiguous usage and budget attribution.
- Q: Should PKCE be mandatory for the OIDC authorization code flow? → A: Yes — PKCE required using `S256` challenge method (OAuth 2.1 compliant); implemented via `oauth2.GenerateVerifier()`.
- Q: How are proxy API keys related to users and teams? → A: Keys are user-bound; each user can create multiple named keys for their own usage tracking. No shared keys, no team-level keys. Team scoping of logs and keys is derived from the user-to-team assignment, not key-to-team assignment.
- Design decision: Three-role model — Global Admin (full access), Team Admin (manages own team, sees all team logs, sets team/member budgets), Member (own logs only + team cost aggregate). Master key = Global Admin equivalent.
- Design decision: Future budget system scoped at org → team → user → key. Data model must accommodate budget limit fields at each level in this iteration even though enforcement is deferred.
- Design decision: Master key login is a break-glass emergency mechanism hidden behind `/login?master=1` when OIDC is configured. Not for routine use; should not appear on the standard login page.

## Assumptions

- OIDC adds a second authentication path to the web UI alongside master key login. Neither replaces the other.
- When OIDC is not configured, the proxy behaves exactly as it does today. OIDC is opt-in.
- A single OIDC provider is configured per deployment. Multi-provider support is out of scope.
- Master key login is a break-glass emergency mechanism, not for day-to-day use. When OIDC is configured, the master key form is hidden on the standard login page and only accessible via `/login?master=1`.
- Master key sessions are always treated as Global Admin-equivalent but are not stored as User records in the database.
- Three roles exist: Global Admin, Team Admin, Member. Master key = Global Admin equivalent.
- Users belong to exactly one team at a time, or are unassigned. Multi-team membership is out of scope.
- Members see only their own logs and keys. They may see their team's aggregate cost data but not individual peer log entries.
- Team Admins see all logs and keys for every member of their team.
- Proxy API keys with no owner user (legacy keys predating OIDC) are visible only to Global Admins and master key sessions.
- When OIDC is disabled after teams/users exist, all data is preserved and master key login remains the only UI access path.
- Budget enforcement is out of scope for this iteration; the data model (schema fields) must be ready to support it.

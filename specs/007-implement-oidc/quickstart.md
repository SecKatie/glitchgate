# Quickstart: Enabling OIDC Authentication

**Feature**: 007-implement-oidc | **Date**: 2026-03-11

---

## Prerequisites

- `llm-proxy` binary built from the `007-implement-oidc` branch or later.
- An OIDC-compatible identity provider (Google, Okta, Auth0, Dex, Keycloak, etc.).
- The proxy must be accessible at a public or network-reachable URL for the
  OIDC redirect callback.

---

## Step 1 — Register the Application with Your Identity Provider

In your IDP, create a new OAuth 2.0 / OIDC application with:

- **Application type**: Web application (server-side, confidential client)
- **Redirect URI**: `https://<your-proxy-host>/ui/auth/callback`
- **Scopes**: `openid`, `email`, `profile`

Note the **Client ID** and **Client Secret** provided by the IDP.

---

## Step 2 — Add OIDC Configuration

Add an `oidc:` block to your `config.yaml`:

```yaml
master_key: "your-existing-master-key"
listen: ":4000"
database_path: "llm-proxy.db"

oidc:
  issuer_url:    "https://accounts.google.com"   # IDP issuer URL
  client_id:     "123456789.apps.googleusercontent.com"
  client_secret: "$OIDC_CLIENT_SECRET"            # env var expansion supported
  redirect_url:  "https://your-proxy.example.com/ui/auth/callback"
  # scopes defaults to [openid, email, profile] — only override if needed
  # scopes:
  #   - openid
  #   - email
  #   - profile
```

Set the client secret via environment variable to avoid committing it:

```bash
export OIDC_CLIENT_SECRET="your-client-secret"
```

---

## Step 3 — Start the Proxy

```bash
./llm-proxy serve --config config.yaml
```

On startup you will see:

```
OIDC provider configured: https://accounts.google.com
llm-proxy listening on :4000
```

If OIDC configuration is missing or the issuer URL is unreachable, the proxy
logs a warning and continues running with master key login only — it does **not**
refuse to start.

---

## Step 4 — Bootstrap the First Admin

Navigate to `https://your-proxy.example.com/ui/login` in a browser.

You will see only the **"Sign In with [Provider]"** button (master key form is
hidden — see below for emergency access).

Click the button, complete authentication at your IDP, and you will be redirected
back to the proxy UI. Because you are the first OIDC user, your account is
automatically created with the **Global Admin** role.

---

## Step 5 — Invite Team Members

As Global Admin:

1. Navigate to **Teams** → create a team.
2. Navigate to **Users** → share the login URL with colleagues.
3. When a colleague logs in for the first time, their account is created as **Member**.
4. Assign them to a team from the Users management page.
5. To delegate team management, promote a member to **Team Admin**.

---

## Emergency / Break-Glass Access

The master key login form is hidden when OIDC is configured. To access it:

```
https://your-proxy.example.com/ui/login?master=1
```

**Use this only for emergencies** (e.g., OIDC provider outage, all admins locked
out). Normal day-to-day access should use OIDC.

---

## Disabling OIDC

Remove or comment out the `oidc:` block from `config.yaml` and restart. All user,
team, and session data is preserved in the database. Only master key login is
available until OIDC is re-enabled.

---

## Configuration Reference

| Key | Required | Description |
|---|---|---|
| `oidc.issuer_url` | Yes | IDP OIDC issuer URL (must serve `/.well-known/openid-configuration`) |
| `oidc.client_id` | Yes | OAuth 2.0 client ID |
| `oidc.client_secret` | Yes | OAuth 2.0 client secret (use env var) |
| `oidc.redirect_url` | Yes | Full callback URL (`https://<host>/ui/auth/callback`) |
| `oidc.scopes` | No | Default: `[openid, email, profile]` |

---

## Tested Identity Providers

| Provider | Issuer URL |
|---|---|
| Google | `https://accounts.google.com` |
| Auth0 | `https://<tenant>.auth0.com/` |
| Okta | `https://<tenant>.okta.com` |
| Keycloak | `https://<host>/realms/<realm>` |
| Dex | `https://<dex-host>` |

Any OIDC provider that serves a standards-compliant discovery document at
`{issuer_url}/.well-known/openid-configuration` will work.

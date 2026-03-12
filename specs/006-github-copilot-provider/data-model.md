# Data Model: GitHub Copilot Provider

**Feature**: 006-github-copilot-provider
**Date**: 2026-03-11

## Entities

### GitHubToken

Persisted GitHub OAuth access token obtained via device flow.

| Field          | Type   | Constraints       | Notes                          |
| -------------- | ------ | ----------------- | ------------------------------ |
| access_token   | string | required, non-empty | GitHub OAuth token (`ghu_...`) |
| token_type     | string | required           | Always `"bearer"`              |
| scope          | string | required           | Always `"read:user"`           |

**Storage**: `~/.config/llm-proxy/copilot/github_token.json` (0600 permissions)
**Lifecycle**: Long-lived. Created by `llm-proxy auth copilot`. Persists across restarts. Only invalidated if user revokes on GitHub.

---

### CopilotSessionToken

Short-lived Copilot API session token exchanged from the GitHub OAuth token.

| Field       | Type   | Constraints           | Notes                                            |
| ----------- | ------ | --------------------- | ------------------------------------------------ |
| token       | string | required, non-empty   | Copilot session token for API calls              |
| expires_at  | int64  | required, Unix epoch  | Expiration timestamp                             |
| api_base    | string | required, URL         | Copilot API base URL from token exchange response |

**Storage**: `~/.config/llm-proxy/copilot/copilot_token.json` (0600 permissions, optional cache)
**Lifecycle**: Short-lived (minutes to hours). Cached to disk for faster proxy restart. Auto-refreshed from GitHubToken when expired.

**State Transitions**:
```
[absent] → [valid] : exchanged from GitHubToken
[valid]  → [expired] : expires_at reached
[expired] → [valid] : re-exchanged from GitHubToken
[valid]  → [invalid] : GitHub token revoked (401 from exchange endpoint)
```

---

### DeviceFlowState (in-memory only)

Transient state during the OAuth device flow, used by `llm-proxy auth copilot`.

| Field             | Type   | Constraints       | Notes                                 |
| ----------------- | ------ | ----------------- | ------------------------------------- |
| device_code       | string | required          | From device code response             |
| user_code         | string | required          | Displayed to operator                 |
| verification_uri  | string | required, URL     | Operator visits this URL              |
| expires_in        | int    | required, seconds | Device code validity window           |
| interval          | int    | required, seconds | Polling interval for token exchange   |

**Storage**: In-memory only (CLI command lifetime)
**Lifecycle**: Created on device code request, consumed during polling, discarded after token obtained or timeout.

---

## Relationships

```
GitHubToken  ──(1:1)──►  CopilotSessionToken
    │                         │
    │  exchanged via          │  used for
    │  /copilot_internal/     │  API calls
    │  v2/token               │
    ▼                         ▼
GitHub API              Copilot API
```

## No Schema Changes

This feature does not modify the SQLite database schema. Copilot requests are logged using the existing `request_logs` table with:
- `provider` = `"github_copilot"` (or the configured provider name)
- `source_format` = `"openai"` or `"anthropic"` (depending on which endpoint the client used)
- Token counts from OpenAI-format response usage field

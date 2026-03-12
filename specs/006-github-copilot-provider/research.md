# Research: GitHub Copilot Provider

**Feature**: 006-github-copilot-provider
**Date**: 2026-03-11

## R-001: GitHub Copilot OAuth Authentication Flow

### Decision
Use the two-step OAuth device flow: (1) GitHub OAuth device flow to get an access token, (2) exchange GitHub token for a Copilot session token.

### Rationale
This is how both litellm and copilot-api implement it. The Copilot API requires a session token obtained from `api.github.com/copilot_internal/v2/token`, not the raw GitHub OAuth token.

### Details

**Step 1 — GitHub OAuth Device Flow:**
- Client ID: `Iv1.b507a08c87ecfe98` (hardcoded, used by all known Copilot proxy implementations)
- Scope: `read:user`
- Device code endpoint: `POST https://github.com/login/device/code`
- Token exchange endpoint: `POST https://github.com/login/oauth/access_token`
- Polling interval: use `interval` from device code response (typically 5s)
- Response contains `access_token` (long-lived) but **no refresh token**

**Step 2 — Copilot Session Token Exchange:**
- Endpoint: `GET https://api.github.com/copilot_internal/v2/token`
- Auth: `Authorization: token <github_access_token>`
- Required headers: editor-version, editor-plugin-version, user-agent
- Response contains:
  - `token`: short-lived Copilot session token (used for API calls)
  - `expires_at`: Unix timestamp
  - `endpoints.api`: the Copilot API base URL (e.g., `https://api.githubcopilot.com`)

**Token Lifecycle:**
- GitHub OAuth token: long-lived, persisted to disk, only needs device flow once
- Copilot session token: short-lived (minutes/hours), cached in memory, auto-refreshed from GitHub token when expired

### Alternatives Considered
- **Single-step auth**: Not possible — Copilot API rejects raw GitHub OAuth tokens
- **VS Code extension auth**: Too tightly coupled to VS Code; device flow is the standard headless approach

---

## R-002: Copilot API Format and Endpoint

### Decision
The Copilot API uses OpenAI-compatible chat completions format at `https://api.githubcopilot.com/chat/completions`.

### Rationale
Confirmed by both litellm and copilot-api implementations. The API accepts standard OpenAI request/response format including streaming via SSE.

### Details
- Endpoint: `POST https://api.githubcopilot.com/chat/completions` (base URL from token exchange response)
- Request format: Standard OpenAI `ChatCompletionRequest`
- Response format: Standard OpenAI `ChatCompletionResponse`
- Streaming: Standard OpenAI SSE format (`data: {...}\n\n` with `data: [DONE]` sentinel)
- Auth header: `Authorization: Bearer <copilot_session_token>`

### Alternatives Considered
- **Anthropic-format endpoint**: Copilot also exposes `/v1/messages` for Anthropic format, but OpenAI format is the primary and most universal path

---

## R-003: Required Editor-Simulation Headers

### Decision
Hardcode four required headers in the provider implementation.

### Rationale
Both litellm and copilot-api use the same header values. These simulate a VS Code editor and are required for the Copilot API to accept requests.

### Details
```
Editor-Version: vscode/1.85.1
Editor-Plugin-Version: copilot/1.155.0
Copilot-Integration-Id: vscode-chat
User-Agent: GithubCopilot/1.155.0
```

### Alternatives Considered
- **Configurable headers**: Adds complexity without benefit; these values are stable across implementations
- **Real editor detection**: Not applicable for a proxy server

---

## R-004: Provider Interface Extension for API Format

### Decision
Add an `APIFormat() string` method to the `provider.Provider` interface returning `"anthropic"` or `"openai"` to indicate the provider's native API format.

### Rationale
The current proxy handlers assume all providers speak Anthropic format. The OpenAI handler translates OpenAI→Anthropic, sends upstream, then translates Anthropic→OpenAI back. For Copilot (which speaks OpenAI natively), this double-translation is wasteful and lossy. The handlers need to know the provider's native format to skip unnecessary translation.

Current flow (all requests go through Anthropic translation):
```
OpenAI client → translate to Anthropic → Anthropic provider → translate back to OpenAI
Anthropic client → send as-is → Anthropic provider → return as-is
```

New flow with format-aware routing:
```
OpenAI client → Anthropic provider: translate → send → translate back
OpenAI client → OpenAI provider (Copilot): send directly → return directly
Anthropic client → Anthropic provider: send as-is → return as-is
Anthropic client → OpenAI provider (Copilot): translate → send → translate back
```

### Details
- `Provider.APIFormat()` returns `"anthropic"` (default for existing Anthropic provider) or `"openai"` (for Copilot)
- OpenAI handler: if provider format is `"openai"`, skip translation and forward directly
- Anthropic handler: if provider format is `"openai"`, translate Anthropic→OpenAI before sending, then OpenAI→Anthropic on response
- Constitution compliant: adding a method to the interface doesn't modify existing implementations (they just need to implement the new method)

### Alternatives Considered
- **Internal translation in provider**: Would duplicate translation logic already in `internal/translate/`
- **Separate handler per provider**: Would duplicate handler logic; format-aware routing is cleaner
- **Always translate through Anthropic**: Works but adds latency and potential data loss for OpenAI-native providers

---

## R-005: Token Storage Format

### Decision
Store tokens as two separate JSON files in `~/.config/llm-proxy/copilot/`:
- `github_token.json`: Long-lived GitHub OAuth access token
- `copilot_token.json`: Cached Copilot session token with expiry (optional, for faster startup)

### Rationale
Separating the two token types matches their different lifecycles. The GitHub token rarely changes; the Copilot session token expires frequently and is easily re-obtained from the GitHub token.

### Details
```json
// github_token.json (0600 permissions)
{
  "access_token": "ghu_...",
  "token_type": "bearer",
  "scope": "read:user"
}

// copilot_token.json (0600 permissions, optional cache)
{
  "token": "tid=...",
  "expires_at": 1741900000,
  "api_base": "https://api.githubcopilot.com"
}
```

Directory created with 0700 permissions. Files created with 0600 permissions.

### Alternatives Considered
- **Single file**: Mixes long-lived and short-lived tokens; unnecessary coupling
- **Encrypted storage**: File permissions provide sufficient protection; encryption adds key management complexity

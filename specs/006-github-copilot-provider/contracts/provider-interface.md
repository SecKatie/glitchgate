# Contract: Provider Interface Extension

**Feature**: 006-github-copilot-provider
**Date**: 2026-03-11

## Provider Interface

The `provider.Provider` interface gains one new method:

```go
type Provider interface {
    Name() string
    AuthMode() string
    APIFormat() string // NEW: returns "anthropic" or "openai"
    SendRequest(ctx context.Context, req *Request) (*Response, error)
}
```

### APIFormat()

Returns the native API format the provider speaks upstream.

| Return Value | Meaning                                      | Providers        |
| ------------ | -------------------------------------------- | ---------------- |
| `"anthropic"` | Provider expects Anthropic Messages format  | Anthropic        |
| `"openai"`    | Provider expects OpenAI Chat Completions format | GitHub Copilot |

### Handler Routing Rules

**`/v1/chat/completions` (OpenAI handler)**:
- If `APIFormat() == "anthropic"`: translate OpenAI→Anthropic, send, translate Anthropic→OpenAI (current behavior)
- If `APIFormat() == "openai"`: forward directly to provider, return response as-is

**`/v1/messages` (Anthropic handler)**:
- If `APIFormat() == "anthropic"`: forward directly to provider, return response as-is (current behavior)
- If `APIFormat() == "openai"`: translate Anthropic→OpenAI, send, translate OpenAI→Anthropic

---

# Contract: Configuration Schema

## Provider Config Extension

```yaml
providers:
  - name: copilot
    type: github_copilot
    token_dir: ~/.config/llm-proxy/copilot  # optional, this is the default
```

New fields on `ProviderConfig`:
- `token_dir` (string, optional): Directory for OAuth token storage. Default: `~/.config/llm-proxy/copilot/`

Fields NOT used by `github_copilot` type:
- `base_url`: Discovered from token exchange response
- `auth_mode`: Always internal (provider manages its own auth)
- `api_key`: Not applicable (uses OAuth)
- `default_version`: Not applicable

## Model Mapping

```yaml
model_list:
  - model_name: "gc/*"
    provider: copilot
    upstream_model: ""  # wildcard: suffix becomes upstream model
```

---

# Contract: CLI Command

## `llm-proxy auth copilot`

### Synopsis
```
llm-proxy auth copilot [--token-dir PATH]
```

### Flags
- `--token-dir`: Override token storage directory (default: `~/.config/llm-proxy/copilot/`)

### Output (stdout)
```
To authenticate with GitHub Copilot, visit:
  https://github.com/login/device

Enter code: XXXX-XXXX

Waiting for authorization...
Authorization successful. Tokens saved to ~/.config/llm-proxy/copilot/
```

### Exit Codes
- `0`: Authentication successful, tokens stored
- `1`: Device flow timed out or user denied authorization
- `1`: Network or API error

### Behavior
1. Checks if valid GitHub token already exists at `token_dir/github_token.json`
   - If valid: prints "Already authenticated" and exits 0
   - If missing/invalid: proceeds with device flow
2. Requests device code from GitHub
3. Displays verification URL and user code
4. Polls for authorization (respects `interval` from response)
5. On success: stores GitHub token, exchanges for Copilot session token, stores both
6. On timeout: prints error, exits 1

---

# Contract: Copilot Provider Upstream Request

## Headers (injected by provider)

```
Authorization: Bearer <copilot_session_token>
Content-Type: application/json
Editor-Version: vscode/1.85.1
Editor-Plugin-Version: copilot/1.155.0
Copilot-Integration-Id: vscode-chat
User-Agent: GithubCopilot/1.155.0
```

## Request Body

Standard OpenAI ChatCompletionRequest (same as existing `internal/translate` types).

## Response Body

Standard OpenAI ChatCompletionResponse with `usage.prompt_tokens` and `usage.completion_tokens`.

## Streaming

Standard OpenAI SSE format:
```
data: {"id":"...","choices":[{"delta":{"content":"Hello"}}]}\n\n
data: {"id":"...","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{...}}\n\n
data: [DONE]\n\n
```

# Research: Core Proxy with Logging & Cost Monitoring

**Feature**: 001-core-proxy-logging
**Date**: 2026-03-11

## Decision 1: Upstream HTTP Client for SSE Streaming

**Decision**: Use `net/http.Client` directly for SSE streaming
requests. Use go-resty/v3 only for non-streaming upstream calls.

**Rationale**: go-resty/v3's `SetDoNotParseResponse(true)` returns
the raw body but the API is not optimized for streaming. The stdlib
`net/http.Client` gives full control over the `io.ReadCloser` body,
which is essential for the line-by-line SSE forwarding loop. Every
production SSE proxy in Go uses the stdlib client directly.

**Alternatives considered**:
- go-resty for all upstream calls: Adds overhead and abstraction
  over the streaming path without benefit.
- `httputil.ReverseProxy`: Cannot inspect, log, or transform the
  stream as it passes through. Not suitable.

## Decision 2: SSE Forwarding Pattern

**Decision**: Manual read/write/flush loop with
`http.ResponseController` (Go 1.20+) for flushing. Use
`bufio.Scanner` for line-oriented SSE reading. Propagate downstream
`r.Context()` to the upstream request for client disconnect handling.

**Rationale**: `ResponseController.Flush()` returns errors (unlike
`http.Flusher`), enabling detection of client disconnects.
`bufio.Scanner` handles SSE line parsing naturally. Context
propagation ensures upstream requests are cancelled when clients
disconnect.

**Key implementation details**:
- Exclude SSE routes from chi's `middleware.Compress` to avoid
  buffering and broken flusher interfaces.
- Set `X-Accel-Buffering: no` header for Nginx compatibility.
- Do NOT use `io.Copy` — it doesn't allow per-chunk flushing.
- Default `bufio.Scanner` 64KB buffer is sufficient for LLM token
  streaming.

## Decision 3: Stream Capture for Logging

**Decision**: Use `io.TeeReader` to capture the full SSE stream
into a `bytes.Buffer` while forwarding to the client. Extract token
counts from Anthropic's `message_start` (input_tokens) and
`message_delta` (output_tokens) events without needing to buffer
the entire response for counting.

**Rationale**: `io.TeeReader` is zero-copy — each read from the
upstream simultaneously writes to the capture buffer. Token counts
are available in specific SSE events, so cost calculation doesn't
require parsing the full response.

**Alternatives considered**:
- Manual dual-write in the loop body: More verbose but offers
  per-event filtering. May be preferable if capture buffers grow
  too large for long responses.
- Capture metadata only (not full response body): Reduces storage
  but loses the ability to inspect full responses in the log viewer.

## Decision 4: Web UI Technology

**Decision**: Go `html/template` + HTMX (~14KB JS) + classless CSS
(Pico CSS ~10KB), all embedded via `go:embed`.

**Rationale**: HTMX enables interactive filtering, pagination, and
sorting without a JavaScript build pipeline or SPA framework. The
server returns HTML fragments, keeping all rendering in Go templates.
The entire UI fits in a single binary with no npm/node_modules.
This aligns with the constitution's emphasis on simplicity and
single-binary deployment.

**Alternatives considered**:
- React/Vue SPA: Requires a build step, node_modules, and increases
  binary size. Overkill for a log table and cost summary.
- Plain HTML with fetch API: Viable but more manual DOM manipulation
  than HTMX. HTMX declarative attributes are simpler.

## Decision 5: Anthropic API Contract

**Decision**: Proxy the `POST /v1/messages` endpoint, supporting
both streaming and non-streaming modes.

**Key schema details**:
- Required headers: `x-api-key`, `anthropic-version`, `content-type`
- Required body fields: `model`, `max_tokens`, `messages`
- Streaming: 8 SSE event types — `message_start`, `ping`,
  `content_block_start`, `content_block_delta`,
  `content_block_stop`, `message_delta`, `message_stop`, `error`
- Token usage: `message_start` contains `input_tokens`,
  `message_delta` contains final `output_tokens`
- Errors: JSON with `type: "error"` wrapper, HTTP status codes
  400/401/403/404/429/500/529

**Proxy behavior**: For Anthropic-compatible requests, the proxy
forwards the request body verbatim (after model name resolution)
and returns the response verbatim. SSE events are forwarded without
transformation.

## Decision 6: OpenAI API Contract

**Decision**: Proxy the `POST /v1/chat/completions` endpoint,
translating to/from Anthropic format.

**Key schema details**:
- Required headers: `Authorization: Bearer`, `Content-Type`
- Required body fields: `model`, `messages`
- Streaming: `data: {json}\n\n` format with `data: [DONE]` sentinel
- Token usage: Available via `stream_options.include_usage: true`
  or in final chunk with empty choices

**Translation mapping** (OpenAI → Anthropic):

| OpenAI | Anthropic |
|--------|-----------|
| `messages[role=system]` | Extracted to `system` field |
| `messages[role=user/assistant]` | Mapped directly |
| `max_tokens` | `max_tokens` |
| `temperature` | `temperature` |
| `top_p` | `top_p` |
| `stop` | `stop_sequences` |
| `tools` (function type) | `tools` (adapted schema) |
| `tool_choice` | `tool_choice` (adapted format) |

**Translation mapping** (Anthropic → OpenAI response):

| Anthropic | OpenAI |
|-----------|--------|
| `stop_reason: "end_turn"` | `finish_reason: "stop"` |
| `stop_reason: "max_tokens"` | `finish_reason: "length"` |
| `stop_reason: "stop_sequence"` | `finish_reason: "stop"` |
| `stop_reason: "tool_use"` | `finish_reason: "tool_calls"` |
| `usage.input_tokens` | `usage.prompt_tokens` |
| `usage.output_tokens` | `usage.completion_tokens` |
| (computed) | `usage.total_tokens` |

**Streaming translation** (Anthropic SSE → OpenAI SSE):

| Anthropic Event | OpenAI Chunk |
|-----------------|--------------|
| `message_start` | Initial chunk with `delta.role` |
| `content_block_delta` (text) | Chunk with `delta.content` |
| `content_block_delta` (tool) | Chunk with `delta.tool_calls` |
| `message_delta` (stop_reason) | Final chunk with `finish_reason` |
| `message_stop` | `data: [DONE]` |

## Decision 7: Session Management

**Decision**: In-memory session store with cryptographically random
tokens and configurable expiry (default 24h). Sessions do not
survive restarts.

**Rationale**: Simple, no additional storage needed. The web UI is
for local/team use — requiring re-login after restart is acceptable.
Uses `crypto/rand` for token generation.

**Alternatives considered**:
- SQLite-backed sessions: Survives restarts but adds complexity
  for minimal benefit in single-tenant deployment.
- JWT tokens: Stateless but harder to revoke. In-memory store
  allows instant revocation.

## Decision 8: Proxy API Key Format

**Decision**: Keys use format `llmp_sk_<32 random hex chars>`.
Stored as bcrypt hashes. First 8 characters (`llmp_sk_`) serve as
the display prefix.

**Rationale**: The `llmp_` prefix makes keys visually identifiable
and scannable by secret detection tools. bcrypt hashing ensures
keys can't be recovered from the database. The prefix is stored
separately for display in logs and the UI without exposing the
full key.

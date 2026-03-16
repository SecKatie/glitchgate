# glitchgate

A self-hosted reverse proxy for LLM APIs with request logging, cost monitoring, team management, and a lightweight web UI.

Routes requests to upstream providers, logs all traffic to a local SQLite database, and calculates per-request costs. Supports Anthropic, OpenAI, and GitHub Copilot upstreams with automatic format translation between API formats.

## Features

- **Multi-Provider Proxy**: Route requests to Anthropic, OpenAI, and GitHub Copilot upstreams
- **Universal API Format**: Clients can use Anthropic, OpenAI Chat Completions, or OpenAI Responses API format regardless of upstream provider
- **Full 3×3 Translation Matrix**: Seamless conversion between all three API formats
- **Authentication**: OIDC/SSO support with user accounts and role-based access (global_admin, team_admin, member)
- **Team Management**: Organize users into teams with shared resources
- **GitHub Copilot Integration**: OAuth device flow authentication (`glitchgate auth copilot`)
- **API Key Management**: Multiple proxy API keys with per-key cost attribution and budget enforcement
- **Wildcard Model Routing**: Prefix-based model matching with fallback chains
- **Budget Enforcement**: Per-key, per-user, per-team, and global spend limits (daily, weekly, monthly)
- **Cost Tracking**: Detailed per-request cost calculation with dashboard visualization
- **Cache Token Logging**: Anthropic prompt caching support
- **Multi-Modal**: Image content handling across all providers
- **Request Security**: Validation, sanitization, and configurable size limits
- **Rate Limiting**: Per-key request throttling with configurable limits
- **Provider Timeouts**: Configurable connection and request deadlines
- **Thinking/Reasoning Tokens**: Extended thinking support for Claude models
- **Web UI**: Embedded HTMX + Pico CSS interface for browsing logs, usage, and managing keys

## Usage

```sh
make build
./glitchgate serve
```

See `make help` or the `cmd/` directory for all available commands. See [docs/configuration.md](docs/configuration.md) for full configuration reference including OIDC setup and GitHub Copilot authentication.

## Recent Changes

- **OIDC/SSO Authentication**: Full user account support with external identity providers
- **Team Management**: Create teams, assign users, and manage team-level resources
- **Budget Enforcement**: Spend limits at global, team, user, and API key scopes
- **OpenAI Upstream Support**: Full proxy support for OpenAI-compatible endpoints
- **Security Hardening**: Request validation, input sanitization, rate limiting, timeouts
- **Cost Query Performance**: Optimized indexing for spend lookups and log pruning

## Development

```sh
make build      # build the binary
make test       # go test -race ./...
make lint       # golangci-lint run
make audit      # gosec + govulncheck
make generate   # sqlc generate
```

## Roadmap

- [ ] **Provider health monitoring** — track errors on a provider and automatically enter temporary cooldown after 429/503 responses before retrying
- [ ] **Per-key UI login** — proxy API key holders can sign in to the web UI to view their own logs and spend
- [ ] **Google GenAI support** — proxy requests to the Google Gemini API with logging and cost attribution
- [ ] **Vertex AI support** — proxy requests to Google Cloud Vertex AI, covering both Gemini models and Anthropic models (Claude on Vertex) with logging and cost attribution
- [ ] **Tiered pricing support** — some models charge different rates above a token threshold (e.g., OpenAI's models billed at higher rates beyond context limits). The `pricing.Entry` struct currently holds a single rate; extend it to support threshold-based tiers so cost calculations remain accurate for long-context requests.

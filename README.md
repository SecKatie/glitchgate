# glitchgate

A self-hosted reverse proxy for LLM APIs with request logging, cost monitoring, team management, and a lightweight web UI.

Supports Anthropic, OpenAI, Gemini, GitHub Copilot, and Vertex AI upstreams with automatic format translation between API formats.

## Quick Start

```bash
# Install
go install github.com/seckatie/glitchgate@latest

# Configure
mkdir -p ~/.config/glitchgate
cat > ~/.config/glitchgate/config.yaml << 'EOF'
master_key: "change-me-to-something-secure"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "$ANTHROPIC_API_KEY"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
EOF

# Run
glitchgate serve
```

Browse to http://localhost:4000/ui and log in with your `master_key`.

See [docs/quickstart.md](docs/quickstart.md) for the full 5-minute setup and [docs/configuration.md](docs/configuration.md) for complete configuration options.

## Features

**Core Proxy**
- Multi-provider support: Anthropic, OpenAI, Gemini, GitHub Copilot, Vertex AI
- Universal API format: Use Anthropic, OpenAI Chat Completions, or OpenAI Responses format with any upstream
- Automatic format translation between all three API styles
- Streaming support (SSE passthrough and synthesis)
- Fallback chains for resilient model routing

**Operations**
- Per-request cost tracking with built-in pricing for major providers
- Budget enforcement at global, team, user, and API key scopes
- Async request logging to SQLite with retention policies
- Rate limiting (per-key and per-IP)
- Prometheus metrics endpoint

**Security**
- OIDC/SSO authentication with PKCE
- Role-based access control (global_admin, team_admin, member)
- Audit logging for sensitive operations
- Input validation and size limits
- CSRF protection and security headers

**Management**
- Web UI for logs, costs, keys, and team administration
- CLI for key management and GitHub Copilot OAuth
- Team management with user assignments
- Wildcard model routing (`prefix/*`)

## Usage

```sh
# Build from source
make build
./glitchgate serve

# Or install directly
go install github.com/seckatie/glitchgate@latest
```

See `make help` for all available commands.

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
- [ ] **Tiered pricing support** — some models charge different rates above a token threshold (e.g., OpenAI's models billed at higher rates beyond context limits). The `pricing.Entry` struct currently holds a single rate; extend it to support threshold-based tiers so cost calculations remain accurate for long-context requests.

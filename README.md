# llm-proxy

A self-hosted reverse proxy for LLM APIs with request logging, cost monitoring, and a lightweight web UI.

Routes requests to upstream providers, logs all traffic to a local SQLite database, and calculates per-request costs. Supports Anthropic and GitHub Copilot upstreams with automatic format translation between API formats.

## Features

- Proxy requests to Anthropic and GitHub Copilot upstreams
- Format-aware routing: clients can use OpenAI or Anthropic format regardless of upstream provider
- GitHub Copilot integration via OAuth device flow (`llm-proxy auth copilot`)
- Multiple proxy API keys with per-key cost attribution
- Wildcard prefix model routing
- Cache token logging (Anthropic prompt caching)
- Cost calculation for a wide range of models (Anthropic, OpenAI, Gemini, Copilot)
- Embedded web UI for browsing logs and usage (protected by a master key)

## Usage

```sh
make build
./llm-proxy serve
```

See `make help` or the `cmd/` directory for all available commands. See [docs/configuration.md](docs/configuration.md) for full configuration reference including GitHub Copilot setup.

## Development

```sh
make build      # build the binary
make test       # go test -race ./...
make lint       # golangci-lint run
make audit      # gosec + govulncheck
make generate   # sqlc generate
```

## Roadmap

- [ ] **Per-key UI login** — proxy API key holders can sign in to the web UI to view their own logs and spend.
- [ ] **OIDC/SSO authentication** — sign in with an external identity provider; enables user accounts with owned API keys and team membership.
- [ ] **Teams & team-level budgets** — group users into teams, assign API keys to a team, and apply shared spend limits at the team level.
- [ ] **Per-key budget enforcement** — optional spend limits (daily, weekly, monthly, or lifetime) per proxy API key; requests from an over-budget key are rejected with a 429. Limits visible and adjustable in the web UI and CLI.
- [ ] **OpenAI endpoint support** — proxy requests to OpenAI upstreams with full request/response logging and cost attribution.
- [ ] **Google GenAI support** — proxy requests to the Google Gemini API with logging and cost attribution.
- [ ] **Vertex AI support** — proxy requests to Google Cloud Vertex AI, covering both Gemini models and Anthropic models (Claude on Vertex) with logging and cost attribution.
- [ ] **Tiered pricing support** — some models charge different rates above a token threshold (e.g. OpenAI's gpt-5.4 is billed at 2× input / 1.5× output beyond 272K context tokens per session). The `pricing.Entry` struct currently holds a single rate; extend it to support threshold-based tiers so cost calculations remain accurate for long-context requests.

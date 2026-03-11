<!--
  Sync Impact Report
  ==================
  Version change: 0.0.0 → 1.0.0 (initial ratification)
  Modified principles: N/A (initial)
  Added sections:
    - Core Principles (5 principles)
    - Technical Constraints
    - Development Workflow
    - Governance
  Removed sections: N/A
  Templates requiring updates:
    - .specify/templates/plan-template.md: ✅ no updates needed (generic)
    - .specify/templates/spec-template.md: ✅ no updates needed (generic)
    - .specify/templates/tasks-template.md: ✅ no updates needed (generic)
  Follow-up TODOs: None
-->

# LLM Proxy Constitution

## Core Principles

### I. Speed Above All

Request latency added by the proxy MUST be negligible relative to
upstream LLM response times. Every code path on the hot path MUST
be profiled and justified. Allocations in the request lifecycle
MUST be minimized — prefer sync.Pool, pre-allocated buffers, and
streaming I/O over convenience copies. Goroutine-per-request is
acceptable; goroutine-per-token is not.

### II. Efficient Use of Resources

Memory and CPU consumption MUST scale linearly (or better) with
concurrent connections. The proxy MUST run comfortably on a single
modest VM (2 vCPU / 2 GB RAM) under typical load. Dependencies
MUST be justified by concrete need — no transitive dependency
bloat. The binary MUST be a single statically-linked executable
with CGO_ENABLED=0 for portable deployment.

### III. Clean Abstractions

Each LLM provider (Anthropic, OpenAI, and future additions) MUST
be behind a well-defined interface boundary. Adding a new provider
MUST NOT require modifying existing provider implementations.
Internal packages MUST have clear, singular responsibilities:
routing, translation, authentication, rate limiting, and
observability are separate concerns. Shared types live in an
internal models package; provider-specific types stay in their
provider package.

### IV. Correctness and Compatibility

The proxy MUST faithfully translate between API formats without
data loss or semantic drift. Streaming responses (SSE) MUST be
forwarded incrementally — never buffered to completion. Error
codes and error shapes from upstream providers MUST be preserved
or mapped to their equivalent in the target API schema. Contract
tests against real provider API schemas MUST exist for every
supported endpoint.

### V. Security by Default

All upstream connections MUST use TLS. API key management MUST
never log or expose secrets in plaintext. Authentication of
incoming requests MUST be enforced by default (no open-by-default
mode). Input validation MUST occur at the edge before any
proxying. The codebase MUST pass `gosec` and `govulncheck` with
zero findings before every release.

## Technical Constraints

- **Language**: Go (latest stable)
- **CLI**: `cobra` + `viper` for configuration
- **Database**: SQLite via `modernc.org/sqlite` (no CGO)
- **HTTP Router**: `chi/v5` (stdlib-compatible, minimal overhead)
- **HTTP Client**: `go-resty/v3` for upstream provider calls
- **Migrations**: `goose/v3`
- **SQL Generation**: `sqlc`
- **Testing**: `go test -race ./...` with `testify/require`
- **Linting**: `golangci-lint` v2 (staticcheck, gosec, errcheck,
  revive, gofumpt enabled)
- **Security**: `gosec`, `govulncheck`, `snyk`
- **Releases**: `goreleaser` with CGO_ENABLED=0
- **License**: AGPL-3.0

Provider interfaces MUST NOT depend on any HTTP framework types.
Translation logic MUST be pure functions operating on internal
model types, making them independently testable without standing
up an HTTP server.

## Development Workflow

- All code MUST be formatted with `gofumpt` and `goimports`.
- All code MUST pass `golangci-lint` with zero warnings before
  commit (enforced via pre-commit hook).
- Tests MUST use table-driven patterns and the race detector.
- Use `require` for preconditions, `assert` for independent checks.
- Commits MUST be atomic and scoped to a single logical change.
- Every new provider MUST include contract tests validating
  request/response translation against the provider's published
  API schema.
- Benchmarks MUST accompany any performance-critical code path
  (request routing, body translation, streaming relay).

## Governance

This constitution is the authoritative source of project
principles. All implementation decisions, code reviews, and
architectural changes MUST be evaluated against these principles.

**Amendment procedure**: Any principle change requires documented
rationale, review of downstream impact on existing specs/plans,
and a version bump following semver:
- MAJOR: Principle removal or incompatible redefinition
- MINOR: New principle or material expansion
- PATCH: Clarification or wording refinement

**Compliance review**: The constitution check in plan-template.md
MUST be completed before any feature design proceeds past the
research phase.

**Version**: 1.0.0 | **Ratified**: 2026-03-11 | **Last Amended**: 2026-03-11

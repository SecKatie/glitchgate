# Implementation Plan: Key Management UI

**Branch**: `005-key-management-ui` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/005-key-management-ui/spec.md`

## Summary

Add a web UI page for managing proxy API keys (list, create, revoke, edit label) and a new CLI `keys update` command for label editing. The feature extends existing HTMX/Pico CSS patterns, adds one new store method (`UpdateKeyLabel`), one new migration for audit logging, and wires up new routes and handlers following established conventions.

## Technical Context

**Language/Version**: Go 1.24+
**Primary Dependencies**: chi/v5 (router), HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), cobra/viper (CLI), modernc.org/sqlite
**Storage**: SQLite via modernc.org/sqlite — one new migration for `audit_events` table
**Testing**: `go test -race ./...` with `testify/require`, table-driven tests
**Target Platform**: Linux/macOS server (single binary, CGO_ENABLED=0)
**Project Type**: CLI + web service (combined binary)
**Performance Goals**: Keys page loads in <2s, key operations complete in <1s server-side
**Constraints**: Single modest VM (2 vCPU / 2 GB), no CGO, AGPL-3.0
**Scale/Scope**: Dozens of keys (not thousands); single-admin typical usage

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Speed Above All | PASS | Key management is not on the hot proxy path. No impact on request latency. |
| II. Efficient Use of Resources | PASS | No new goroutines or memory-intensive patterns. Single SQL query per operation. |
| III. Clean Abstractions | PASS | New store method follows existing interface. Web handlers in existing `web` package. CLI command follows cobra pattern. |
| IV. Correctness and Compatibility | PASS | No translation or streaming changes. Existing proxy behavior unaffected. |
| V. Security by Default | PASS | Key management requires session auth. Plaintext shown once, never stored. Label edits validated. Audit trail for create/revoke. `gosec`/`govulncheck` gates apply. |

No violations. No complexity tracking needed.

## Project Structure

### Documentation (this feature)

```text
specs/005-key-management-ui/
├── spec.md              # Feature specification
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── ui-api.md        # Web API endpoint contracts
│   └── cli.md           # CLI command contracts
└── tasks.md             # Phase 2 output (via /speckit.tasks)
```

### Source Code (repository root)

```text
cmd/
├── keys.go              # MODIFY: add keysUpdateCmd
└── serve.go             # MODIFY: register key management routes

internal/
├── store/
│   ├── store.go         # MODIFY: add UpdateKeyLabel + audit methods to interface
│   ├── sqlite.go        # MODIFY: implement UpdateKeyLabel + audit methods
│   └── migrations/
│       └── 005_create_audit_events.sql  # NEW: audit_events table
├── web/
│   ├── handlers.go      # MODIFY: add key management handler methods
│   └── templates/
│       ├── layout.html  # MODIFY: add Keys nav link
│       ├── keys.html    # NEW: keys page template
│       └── fragments/
│           └── key_rows.html  # NEW: HTMX fragment for key list
```

**Structure Decision**: All changes fit within the existing project structure. No new packages needed — key management handlers go in the existing `web.Handlers` struct, store methods extend the existing `Store` interface, CLI commands extend the existing `keysCmd` parent.

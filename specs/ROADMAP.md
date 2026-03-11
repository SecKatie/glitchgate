# LLM Proxy Roadmap

Items captured during specification and clarification sessions.
These are out of scope for the current MVP but inform future work.

## Post-MVP Features

### Authentication & Access Control

- **Per-key UI login**: Each proxy API key can log into the web UI
  to view only its own logs and usage data (scoped views).

### Routing & Reliability

- **Dynamic routing with fallbacks**: Route requests across multiple
  upstream providers with automatic fallback when a provider is
  unavailable or returns errors.

### Budget & Rate Limiting

- **Per-key budget enforcement**: Set spending limits per proxy API
  key; reject requests when the budget is exhausted.
- **Per-key rate limiting**: Throttle requests per API key to
  prevent runaway usage.

### Provider Expansion

- **OpenAI as upstream provider**: Add OpenAI as a second upstream
  provider type, enabling model mappings that route to OpenAI's API.
- **Additional upstream providers**: Extend the provider interface
  to support other LLM services (Google, Mistral, etc.).

### Operational

- **Automatic model pricing updates**: Fetch current pricing from
  provider APIs instead of manual configuration.
- **Log retention policies**: Auto-purge logs older than a
  configurable threshold.
- **Remote/distributed storage**: Support external databases for
  log storage in larger deployments.

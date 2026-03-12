# Quickstart: Fallback Models

## Configuring a virtual model

Add an entry to `model_list` with a `fallbacks` array instead of `provider`/`upstream_model`:

```yaml
model_list:
  # Existing direct entries
  - model_name: "claude-haiku"
    provider: "anthropic-primary"
    upstream_model: "claude-3-5-haiku-20241022"

  - model_name: "sonnet-primary"
    provider: "anthropic-primary"
    upstream_model: "claude-3-5-sonnet-20241022"

  - model_name: "sonnet-secondary"
    provider: "anthropic-backup"
    upstream_model: "claude-3-5-sonnet-20241022"

  # Virtual model: tries sonnet-primary first, falls back to sonnet-secondary
  - model_name: "sonnet"
    fallbacks:
      - "sonnet-primary"
      - "sonnet-secondary"

  # Single-entry virtual: pure indirection for easy provider swaps
  - model_name: "smart"
    fallbacks:
      - "sonnet-primary"
```

Clients call `"sonnet"` or `"smart"` exactly as they would any direct model. No client changes are needed.

## What triggers a fallback?

| Condition | Fallback? |
|-----------|-----------|
| Provider returns 5xx | Yes |
| Provider returns 429 (rate limited) | Yes |
| Network error / timeout | Yes |
| Provider returns 4xx (other) | No — error returned to client immediately |
| First entry succeeds | No — response returned immediately |

## Reading fallback activity in logs

Each request log entry shows `fallback_attempts`:
- `1` — first choice succeeded (or a direct model was used)
- `2+` — that many providers were tried before success or exhaustion

## Validation errors at startup

| Config mistake | Error message |
|----------------|---------------|
| Both `provider` and `fallbacks` set | `model "X": cannot set both provider/upstream_model and fallbacks` |
| Unknown name in `fallbacks` | `model "X": fallback "Y" is not defined in model_list` |
| Circular reference | `model "X": circular fallback reference detected: X → Y → X` |

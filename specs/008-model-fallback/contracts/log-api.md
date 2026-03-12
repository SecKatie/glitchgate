# Contract: Request Log API — fallback_attempts field

**Feature**: 008-model-fallback
**Affected endpoints**: `GET /logs` (list), `GET /logs/:id` (detail)

---

## New field

Both the list summary and the detail response gain a `fallback_attempts` field.

### List response (`GET /logs`)

```json
{
  "logs": [
    {
      "id": "...",
      "model_requested": "smart-resilient",
      "model_upstream": "claude-3-5-sonnet-20241022",
      "provider_name": "anthropic-secondary",
      "fallback_attempts": 2,
      ...
    }
  ]
}
```

### Detail response (`GET /logs/:id`)

Same `fallback_attempts` field present on the detail object.

---

## Semantics

| Value | Meaning |
|-------|---------|
| `1` | First-choice provider succeeded (or request used a direct model — no fallback involved) |
| `2` | First choice failed; second entry succeeded |
| `N` | First `N-1` entries failed; Nth entry succeeded |
| `N` where all failed | All `N` entries in the chain were attempted and all failed |

## Backward compatibility

All existing log rows default to `fallback_attempts = 1`. No existing API consumers need to change; the field is additive.

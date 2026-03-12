# Contract: Config Schema — model_list

**Feature**: 008-model-fallback
**Endpoint**: YAML configuration file (`config.yaml`)

---

## Overview

The `model_list` array supports two mutually exclusive entry forms. Existing direct entries are unchanged. Virtual entries use `fallbacks` in place of `provider`/`upstream_model`.

---

## Direct Entry (unchanged)

```yaml
model_list:
  - model_name: <string>       # required; client-facing name
    provider: <string>         # required; must match a name in providers[]
    upstream_model: <string>   # required; model name sent upstream
```

## Virtual Entry (new)

```yaml
model_list:
  - model_name: <string>       # required; client-facing name
    fallbacks:                 # required; at least one entry
      - <model_name>           # references another model_list entry (direct or virtual)
      - <model_name>
```

## Validation errors (startup)

| Condition | Error |
|-----------|-------|
| Both `provider`/`upstream_model` and `fallbacks` set | `model "X": cannot set both provider/upstream_model and fallbacks` |
| Neither `provider`/`upstream_model` nor `fallbacks` set | `model "X": must set either provider/upstream_model or fallbacks` |
| `fallbacks` references unknown model name | `model "X": fallback "Y" is not defined in model_list` |
| Circular reference | `model "X": circular fallback reference detected: X → Y → X` |
| Duplicate `model_name` | `duplicate model_name "X" in model_list` |

## Precedence

Wildcard (`/*`) matching continues to apply only to direct entries. Virtual entries do not support wildcards in `model_name` or within `fallbacks`.

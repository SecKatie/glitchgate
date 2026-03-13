# Quickstart: Models Page

**Branch**: `011-models-page` | **Date**: 2026-03-13

## What This Feature Adds

A **Models** page in the glitchgate web UI at `/ui/models`. It shows all models configured in `model_list` with pricing and usage at a glance, plus a detail page for each model.

## Seeing It In Action

1. Start glitchgate with a config that has models in `model_list`:

```yaml
model_list:
  - model_name: claude-sonnet
    provider: anthropic
    upstream_model: claude-sonnet-4-20250514

  - model_name: fast
    fallbacks:
      - claude-haiku
      - claude-sonnet

  - model_name: gc/*
    provider: copilot
```

2. Log in to the web UI and click **Models** in the nav.

3. You'll see a table with each configured model, its provider, and pricing rates.

4. Click any model name to open its detail page.

## Detail Page Sections

| Section | Content |
|---------|---------|
| **Pricing** | Input, Output, Cache Write, Cache Read rates per million tokens. Shows "—" if no pricing is configured. |
| **Usage** | Total request count, input tokens, output tokens, and estimated cost from all-time request logs. |
| **Provider** | Provider name and type (Anthropic, OpenAI, GitHub Copilot, etc.). |
| **Fallback Chain** | Ordered list of concrete models (virtual models only). |
| **Example Request** | Copy-ready `curl` command targeting `/v1/messages` with this model. |

## Notes

- Models removed from config do not appear on this page. Historical usage for removed models is still visible in Logs and Costs.
- Wildcard entries (e.g., `gc/*`) appear as a single row. Usage stats for wildcards show N/A since each request uses a specific resolved name.
- The curl example uses a placeholder key (`YOUR_PROXY_KEY`) — substitute a real proxy key from the Keys page.

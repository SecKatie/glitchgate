# Web Routes Contract: Models Page

**Branch**: `011-models-page` | **Date**: 2026-03-13

## Routes

All routes require an authenticated session (enforced by existing `RequireAuth` middleware).

### GET /ui/models

**Purpose**: Render the model list page.

**Handler**: `Handlers.ModelsPage`

**Response**: `200 OK`, `text/html; charset=utf-8`

**Template**: `models.html`

**Data**:
```
ActiveTab   = "models"
Models      = []ModelListItem   // one entry per config.ModelMapping
```

**Error cases**:
- `500 Internal Server Error` if template rendering fails (logged, never exposed)

---

### GET /ui/models/{model}

**Purpose**: Render the detail page for a single model.

**Handler**: `Handlers.ModelDetailPage`

**Route registration** (chi): `/ui/models/*` catch-all to support slashes in model names (e.g., `gc/claude-sonnet`). The handler extracts the model name from `chi.URLParam(r, "*")`.

**Path parameter**: `model` — URL-encoded model name (decoded by the handler with `url.PathUnescape`)

**Response**: `200 OK`, `text/html; charset=utf-8`

**Template**: `model_detail.html`

**Data**:
```
ActiveTab     = "models"
ModelName     = string
ProviderName  = string
ProviderType  = string
IsVirtual     = bool
IsWildcard    = bool
Fallbacks     = []string
Pricing       = *pricing.Entry (nil if unknown)
HasPricing    = bool
Usage         = *store.ModelUsageSummary
CurlExample   = string
UpstreamModel = string
```

**Error cases**:
- `404 Not Found` — model name not found in `config.ModelList`
- `500 Internal Server Error` — store query failure or template rendering failure

---

## Navigation

The `layout.html` nav bar gains a new entry:

```html
<li>
  <a href="/ui/models"
     {{if eq .ActiveTab "models"}} aria-current="page" class="nav-active"{{end}}>
    Models
  </a>
</li>
```

Inserted after the "Costs" entry (before "Keys"), matching the informational grouping of Logs/Costs/Models.

---

## curl Example Format

The curl example on the detail page is a pre-formatted string built in the handler:

```
curl https://your-glitchgate-host/v1/messages \
  -H "x-api-key: YOUR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model_name>",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

The placeholder `YOUR_PROXY_KEY` is hardcoded — no real credentials are ever rendered.

A note below the example states: "The OpenAI-compatible endpoint (`/v1/chat/completions`) is also supported with the same proxy key."

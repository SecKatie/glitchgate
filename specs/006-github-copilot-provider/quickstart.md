# Quickstart: GitHub Copilot Provider

**Feature**: 006-github-copilot-provider

## Prerequisites

- Active GitHub Copilot subscription (Individual, Business, or Enterprise)
- llm-proxy binary built with Copilot provider support

## Setup

### 1. Authenticate with GitHub Copilot

```bash
llm-proxy auth copilot
```

Follow the prompts to authorize via GitHub's device flow. Tokens are saved to `~/.config/llm-proxy/copilot/`.

### 2. Configure the provider

Add to your `config.yaml`:

```yaml
providers:
  - name: copilot
    type: github_copilot

model_list:
  - model_name: "gc/*"
    provider: copilot
    upstream_model: ""
```

### 3. Start the proxy

```bash
llm-proxy serve
```

### 4. Send requests

**OpenAI format** (direct):
```bash
curl -X POST http://localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer llmp_sk_YOUR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gc/claude-sonnet-4.6",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Anthropic format** (translated automatically):
```bash
curl -X POST http://localhost:4000/v1/messages \
  -H "X-Api-Key: llmp_sk_YOUR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gc/claude-sonnet-4.6",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 1024
  }'
```

## Available Models

Any model available on your Copilot subscription can be used with the `gc/` prefix:

| Model Name               | Description                      |
| ------------------------ | -------------------------------- |
| `gc/claude-opus-4.6`     | Claude Opus 4.6 via Copilot      |
| `gc/claude-sonnet-4.6`   | Claude Sonnet 4.6 via Copilot    |
| `gc/gpt-5.2`             | GPT-5.2 via Copilot              |
| `gc/gemini-3.1-pro-preview` | Gemini 3.1 Pro via Copilot    |

## Re-authentication

If your GitHub token is revoked or expires, re-run:

```bash
llm-proxy auth copilot
```

The proxy will return a clear error message if authentication is needed.

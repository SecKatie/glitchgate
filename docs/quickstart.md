# Quickstart

Get glitchgate running with a single upstream provider in under 5 minutes.

## Install

```bash
go install github.com/seckatie/glitchgate@latest
```

This installs `glitchgate` to `$GOPATH/bin` or `$HOME/go/bin`.

## Configure

Create the config directory and a minimal config:

```bash
mkdir -p ~/.config/glitchgate
cat > ~/.config/glitchgate/config.yaml << 'EOF'
master_key: "change-me-to-something-secure"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "$ANTHROPIC_API_KEY"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
EOF
```

Set your Anthropic API key:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

## Start the Server

```bash
glitchgate serve
```

You should see:
```
2026/03/17 10:30:00 glitchgate listening on :4000
```

## Create a Proxy Key

In another terminal:

```bash
export GLITCHGATE_MASTER_KEY="change-me-to-something-secure"
glitchgate keys create --name my-key
```

Save the output — it looks like `llmp_sk_...` and cannot be shown again.

## Make a Test Request

```bash
export GLITCHGATE_API_KEY="llmp_sk_..."  # the key you just created

curl -X POST http://localhost:4000/v1/messages \
  -H "Content-Type: application/json" \
  -H "X-Proxy-Api-Key: $GLITCHGATE_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello, world!"}]
  }'
```

## Open the Web UI

Navigate to [http://localhost:4000/ui](http://localhost:4000/ui) and log in with your `master_key`.

You should see your request in the logs with token counts and cost.

## Next Steps

- **Add fallback models**: Configure a virtual model that tries multiple providers
- **Enable OIDC/SSO**: Add `oidc:` section for team access
- **Set budgets**: Configure per-key or global spend limits
- **See [configuration.md](configuration.md)** for the full reference

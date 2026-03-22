package pricing

import (
	"net/url"
	"strings"
)

// AnthropicDefaults holds pricing for Claude models billed at official Anthropic API rates.
// Applied only for providers with type "anthropic" whose base_url matches the official endpoint.
// Values are USD per million tokens as of 2026-03-11.
//
// Cache semantics: CacheWrite is 1.25× input (5-min TTL); CacheRead is 0.10× input.
var AnthropicDefaults = map[string]Entry{
	// Claude 4.6 (latest)
	"claude-opus-4-6": {
		InputPerMillion:      5.00,
		OutputPerMillion:     25.00,
		CacheWritePerMillion: 6.25,
		CacheReadPerMillion:  0.50,
		ContextWindow:        1000000, // 1M tokens
		MaxTokens:            128000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-sonnet-4-6": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
		ContextWindow:        1000000, // 1M tokens
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	// Claude Haiku 4.5 (latest — dated ID + alias)
	"claude-haiku-4-5-20251001": {
		InputPerMillion:      1.00,
		OutputPerMillion:     5.00,
		CacheWritePerMillion: 1.25,
		CacheReadPerMillion:  0.10,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-haiku-4-5": {
		InputPerMillion:      1.00,
		OutputPerMillion:     5.00,
		CacheWritePerMillion: 1.25,
		CacheReadPerMillion:  0.10,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	// Claude 4.5
	"claude-opus-4-5-20251101": {
		InputPerMillion:      5.00,
		OutputPerMillion:     25.00,
		CacheWritePerMillion: 6.25,
		CacheReadPerMillion:  0.50,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-opus-4-5": {
		InputPerMillion:      5.00,
		OutputPerMillion:     25.00,
		CacheWritePerMillion: 6.25,
		CacheReadPerMillion:  0.50,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-sonnet-4-5-20250929": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-sonnet-4-5": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	// Claude 4.1 (legacy)
	"claude-opus-4-1-20250805": {
		InputPerMillion:      15.00,
		OutputPerMillion:     75.00,
		CacheWritePerMillion: 18.75,
		CacheReadPerMillion:  1.50,
		ContextWindow:        200000,
		MaxTokens:            32000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-opus-4-1": {
		InputPerMillion:      15.00,
		OutputPerMillion:     75.00,
		CacheWritePerMillion: 18.75,
		CacheReadPerMillion:  1.50,
		ContextWindow:        200000,
		MaxTokens:            32000,
		Reasoning:            true,
		Vision:               true,
	},
	// Claude 4 (legacy)
	"claude-sonnet-4-20250514": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
		ContextWindow:        200000,
		MaxTokens:            64000,
		Reasoning:            true,
		Vision:               true,
	},
	"claude-opus-4-20250514": {
		InputPerMillion:      15.00,
		OutputPerMillion:     75.00,
		CacheWritePerMillion: 18.75,
		CacheReadPerMillion:  1.50,
		ContextWindow:        200000,
		MaxTokens:            32000,
		Reasoning:            true,
		Vision:               true,
	},
	// Claude 3.5 Haiku (legacy)
	"claude-3-5-haiku-20241022": {
		InputPerMillion:      0.80,
		OutputPerMillion:     4.00,
		CacheWritePerMillion: 1.00,
		CacheReadPerMillion:  0.08,
		ContextWindow:        200000,
		MaxTokens:            8192,
		Reasoning:            false,
		Vision:               true,
	},
	// Claude 3 Haiku (legacy)
	"claude-3-haiku-20240307": {
		InputPerMillion:      0.25,
		OutputPerMillion:     1.25,
		CacheWritePerMillion: 0.30,
		CacheReadPerMillion:  0.03,
		ContextWindow:        200000,
		MaxTokens:            4096,
		Reasoning:            false,
		Vision:               true,
	},
}

// CopilotDefaults holds $0 pricing for GitHub Copilot models.
// Applied for all providers with type "github_copilot".
// Copilot is subscription-billed; entries exist so requests are tracked without inflating costs.
var CopilotDefaults = map[string]Entry{
	"claude-opus-4.6":   {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 1000000, MaxTokens: 128000, Reasoning: true, Vision: true},
	"claude-sonnet-4.6": {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 1000000, MaxTokens: 64000, Reasoning: true, Vision: true},
	"gpt-5.2":           {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 400000, MaxTokens: 128000, Reasoning: true, Vision: true},
	"gpt-5.4":           {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 1050000, MaxTokens: 128000, Reasoning: true, Vision: true},
	"gpt-5.4-pro":       {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 1050000, MaxTokens: 128000, Reasoning: true, Vision: true},
	"gpt-5-mini":        {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 400000, MaxTokens: 128000, Reasoning: true, Vision: true},
	"o4-mini":           {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 200000, MaxTokens: 100000, Reasoning: true, Vision: true},
	"o3":                {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 200000, MaxTokens: 100000, Reasoning: true, Vision: true},
	"gemini-3-flash":    {InputPerMillion: 0, OutputPerMillion: 0, ContextWindow: 1000000, MaxTokens: 65536, Reasoning: true, Vision: true},
}

// OpenAIDefaults holds pricing for OpenAI models billed at official API-equivalent rates.
// Applied only for providers with type "openai" or "openai_responses" whose
// base_url matches the official API endpoint or the ChatGPT Codex backend.
// Values are USD per million tokens as of 2026-03-13.
var OpenAIDefaults = map[string]Entry{
	"gpt-4o": {
		InputPerMillion:     2.50,
		OutputPerMillion:    10.00,
		CacheReadPerMillion: 1.25,
		ContextWindow:       128000,
		MaxTokens:           16384,
		Vision:              true,
	},
	"gpt-4o-mini": {
		InputPerMillion:     0.15,
		OutputPerMillion:    0.60,
		CacheReadPerMillion: 0.075,
		ContextWindow:       128000,
		MaxTokens:           16384,
		Vision:              true,
	},
	"gpt-4.1": {
		InputPerMillion:     2.00,
		OutputPerMillion:    8.00,
		CacheReadPerMillion: 0.50,
		ContextWindow:       1047576,
		MaxTokens:           32768,
		Vision:              true,
	},
	"gpt-4.1-mini": {
		InputPerMillion:     0.40,
		OutputPerMillion:    1.60,
		CacheReadPerMillion: 0.10,
		ContextWindow:       1047576,
		MaxTokens:           32768,
		Vision:              true,
	},
	"gpt-4.1-nano": {
		InputPerMillion:     0.10,
		OutputPerMillion:    0.40,
		CacheReadPerMillion: 0.025,
		ContextWindow:       1047576,
		MaxTokens:           32768,
		Vision:              true,
	},
	"gpt-5.4": {
		InputPerMillion:       2.50,
		OutputPerMillion:      15.00,
		CacheReadPerMillion:   0.25,
		ContextWindow:         1050000,
		StandardContextWindow: 272000, // >272K input: 2× input, 1.5× output
		MaxTokens:             128000,
		Reasoning:             true,
		Vision:                true,
	},
	"gpt-5-mini": {
		InputPerMillion:     0.25,
		OutputPerMillion:    2.00,
		CacheReadPerMillion: 0.025,
		ContextWindow:       400000,
		MaxTokens:           128000,
		Reasoning:           true,
		Vision:              true,
	},
	"o3": {
		InputPerMillion:  2.00,
		OutputPerMillion: 8.00,
		ContextWindow:    200000,
		MaxTokens:        100000,
		Reasoning:        true,
		Vision:           true,
	},
	"o3-mini": {
		InputPerMillion:  1.10,
		OutputPerMillion: 4.40,
		ContextWindow:    200000,
		MaxTokens:        100000,
		Reasoning:        true,
	},
	"o4-mini": {
		InputPerMillion:  1.10,
		OutputPerMillion: 4.40,
		ContextWindow:    200000,
		MaxTokens:        100000,
		Reasoning:        true,
		Vision:           true,
	},
}

// ChutesDefaults holds pricing for models accessed via the Chutes AI inference platform.
// Applied for providers with type "openai" whose base_url targets llm.chutes.ai.
// Values are USD per million tokens.
var ChutesDefaults = map[string]Entry{
	"zai-org/GLM-5-TEE": {
		InputPerMillion:  0.95,
		OutputPerMillion: 3.15,
		ContextWindow:    128000,
		MaxTokens:        16384,
	},
	"moonshotai/Kimi-K2.5-TEE": {
		InputPerMillion:  0.45,
		OutputPerMillion: 2.20,
		ContextWindow:    256000,
		MaxTokens:        32768,
		Reasoning:        true,
		Vision:           true,
	},
	"MiniMaxAI/MiniMax-M2.5-TEE": {
		InputPerMillion:  0.30,
		OutputPerMillion: 1.10,
		ContextWindow:    187000,
		MaxTokens:        32768,
		Vision:           true,
	},
	"deepseek-ai/DeepSeek-V3.2-TEE": {
		InputPerMillion:  0.28,
		OutputPerMillion: 0.42,
		ContextWindow:    159000,
		MaxTokens:        16384,
	},
	"deepseek-ai/DeepSeek-R1-0528-TEE": {
		InputPerMillion:  0.45,
		OutputPerMillion: 2.15,
		ContextWindow:    128000,
		MaxTokens:        163840,
		Reasoning:        true,
	},
	"Qwen/Qwen3-Coder-Next-TEE": {
		InputPerMillion:  0.12,
		OutputPerMillion: 0.75,
		ContextWindow:    256000,
		MaxTokens:        16384,
	},
}

// SyntheticDefaults holds pricing for models accessed via the Synthetic.new platform.
// Applied for providers with type "openai" whose base_url targets api.synthetic.new.
// Values are USD per million tokens as of 2026-03-20.
var SyntheticDefaults = map[string]Entry{
	// DeepSeek (via Together AI)
	"hf:deepseek-ai/DeepSeek-R1-0528": {
		InputPerMillion:  3.00,
		OutputPerMillion: 8.00,
		ContextWindow:    128000,
		MaxTokens:        163840,
		Reasoning:        true,
	},
	"hf:deepseek-ai/DeepSeek-V3": {
		InputPerMillion:  1.25,
		OutputPerMillion: 1.25,
		ContextWindow:    128000,
		MaxTokens:        8192,
	},
	// DeepSeek (via Fireworks)
	"hf:deepseek-ai/DeepSeek-V3.2": {
		InputPerMillion:  0.56,
		OutputPerMillion: 1.68,
		ContextWindow:    159000,
		MaxTokens:        16384,
	},
	// Meta (via Together AI)
	"hf:meta-llama/Llama-3.3-70B-Instruct": {
		InputPerMillion:  0.88,
		OutputPerMillion: 0.88,
		ContextWindow:    128000,
		MaxTokens:        4096,
	},
	// MiniMax (via Fireworks)
	"hf:MiniMaxAI/MiniMax-M2.1": {
		InputPerMillion:  0.30,
		OutputPerMillion: 1.20,
		ContextWindow:    192000,
		MaxTokens:        32768,
		Reasoning:        true,
	},
	// MiniMax (via Synthetic)
	"hf:MiniMaxAI/MiniMax-M2.5": {
		InputPerMillion:  0.40,
		OutputPerMillion: 2.00,
		ContextWindow:    187000,
		MaxTokens:        32768,
		Vision:           true,
	},
	// Moonshot / Kimi (via Fireworks)
	"hf:moonshotai/Kimi-K2-Instruct-0905": {
		InputPerMillion:  1.20,
		OutputPerMillion: 1.20,
		ContextWindow:    131000,
		MaxTokens:        131000,
	},
	"hf:moonshotai/Kimi-K2-Thinking": {
		InputPerMillion:  0.60,
		OutputPerMillion: 2.50,
		ContextWindow:    131000,
		MaxTokens:        262000,
		Reasoning:        true,
	},
	// Moonshot / Kimi (via Synthetic)
	"hf:moonshotai/Kimi-K2.5": {
		InputPerMillion:  0.60,
		OutputPerMillion: 3.00,
		ContextWindow:    256000,
		MaxTokens:        32768,
		Reasoning:        true,
		Vision:           true,
	},
	// NVIDIA (via Synthetic)
	"hf:nvidia/Kimi-K2.5-NVFP4": {
		InputPerMillion:  0.60,
		OutputPerMillion: 3.00,
		ContextWindow:    256000,
		MaxTokens:        32768,
		Reasoning:        true,
		Vision:           true,
	},
	"hf:nvidia/NVIDIA-Nemotron-3-Super-120B-A12B-NVFP4": {
		InputPerMillion:  0.30,
		OutputPerMillion: 1.00,
		ContextWindow:    256000,
		MaxTokens:        4096,
	},
	// OpenAI (via Fireworks)
	"hf:openai/gpt-oss-120b": {
		InputPerMillion:  0.10,
		OutputPerMillion: 0.10,
		ContextWindow:    128000,
		MaxTokens:        4096,
	},
	// Qwen (via Together AI)
	"hf:Qwen/Qwen3-235B-A22B-Thinking-2507": {
		InputPerMillion:  0.65,
		OutputPerMillion: 3.00,
		ContextWindow:    256000,
		MaxTokens:        32768,
		Reasoning:        true,
	},
	"hf:Qwen/Qwen3-Coder-480B-A35B-Instruct": {
		InputPerMillion:  2.00,
		OutputPerMillion: 2.00,
		ContextWindow:    256000,
		MaxTokens:        16384,
	},
	"hf:Qwen/Qwen3.5-397B-A17B": {
		InputPerMillion:  0.60,
		OutputPerMillion: 3.60,
		ContextWindow:    256000,
		MaxTokens:        32768,
		Reasoning:        true,
	},
	// Zhipu AI / GLM (via Synthetic)
	"hf:zai-org/GLM-4.7": {
		InputPerMillion:  0.55,
		OutputPerMillion: 2.19,
		ContextWindow:    200000,
		MaxTokens:        128000,
		Reasoning:        true,
	},
	"hf:zai-org/GLM-4.7-Flash": {
		InputPerMillion:  0.10,
		OutputPerMillion: 0.50,
		ContextWindow:    192000,
		MaxTokens:        16384,
	},
}

// GeminiDefaults holds pricing for Gemini models on native Gemini upstreams.
// Applied for providers with type "gemini".
// Values are USD per million tokens (standard tier, <=200K context) as of 2026-03-21.
var GeminiDefaults = map[string]Entry{
	// Gemini 3.1
	"google/gemini-3.1-pro-preview": {
		InputPerMillion:       2.00,
		OutputPerMillion:      12.00,
		CacheReadPerMillion:   0.20,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"google/gemini-3.1-lite-preview": {
		InputPerMillion:     0.25,
		OutputPerMillion:    1.50,
		CacheReadPerMillion: 0.025,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"google/gemini-3.1-flash-lite-preview": {
		InputPerMillion:  0.50,
		OutputPerMillion: 3.00,
		ContextWindow:    1048576,
		MaxTokens:        65536,
		Reasoning:        true,
		Vision:           true,
	},
	// Gemini 3
	"google/gemini-3-pro-preview": {
		InputPerMillion:       2.00,
		OutputPerMillion:      12.00,
		CacheReadPerMillion:   0.20,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"google/gemini-3-flash-preview": {
		InputPerMillion:     0.50,
		OutputPerMillion:    3.00,
		CacheReadPerMillion: 0.05,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	// Gemini 2.5
	"google/gemini-2.5-pro": {
		InputPerMillion:       1.25,
		OutputPerMillion:      10.00,
		CacheReadPerMillion:   0.13,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"google/gemini-2.5-flash": {
		InputPerMillion:     0.30,
		OutputPerMillion:    2.50,
		CacheReadPerMillion: 0.03,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"google/gemini-2.5-flash-lite": {
		InputPerMillion:     0.10,
		OutputPerMillion:    0.40,
		CacheReadPerMillion: 0.01,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	// Gemini 2.0
	"google/gemini-2.0-flash": {
		InputPerMillion:  0.10,
		OutputPerMillion: 0.40,
		ContextWindow:    1048576,
		MaxTokens:        8192,
		Vision:           true,
	},
	"google/gemini-2.0-flash-lite": {
		InputPerMillion:  0.075,
		OutputPerMillion: 0.30,
		ContextWindow:    1048576,
		MaxTokens:        8192,
	},
	"gemini-3.1-pro-preview": {
		InputPerMillion:       2.00,
		OutputPerMillion:      12.00,
		CacheReadPerMillion:   0.20,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"gemini-3.1-lite-preview": {
		InputPerMillion:     0.25,
		OutputPerMillion:    1.50,
		CacheReadPerMillion: 0.025,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"gemini-3.1-flash-lite-preview": {
		InputPerMillion:  0.50,
		OutputPerMillion: 3.00,
		ContextWindow:    1048576,
		MaxTokens:        65536,
		Reasoning:        true,
		Vision:           true,
	},
	"gemini-3-pro-preview": {
		InputPerMillion:       2.00,
		OutputPerMillion:      12.00,
		CacheReadPerMillion:   0.20,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"gemini-3-flash-preview": {
		InputPerMillion:     0.50,
		OutputPerMillion:    3.00,
		CacheReadPerMillion: 0.05,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"gemini-2.5-pro": {
		InputPerMillion:       1.25,
		OutputPerMillion:      10.00,
		CacheReadPerMillion:   0.13,
		ContextWindow:         1048576,
		StandardContextWindow: 200000, // >200K: 2× input, 1.5× output
		MaxTokens:             65536,
		Reasoning:             true,
		Vision:                true,
	},
	"gemini-2.5-flash": {
		InputPerMillion:     0.30,
		OutputPerMillion:    2.50,
		CacheReadPerMillion: 0.03,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"gemini-2.5-flash-lite": {
		InputPerMillion:     0.10,
		OutputPerMillion:    0.40,
		CacheReadPerMillion: 0.01,
		ContextWindow:       1048576,
		MaxTokens:           65536,
		Reasoning:           true,
		Vision:              true,
	},
	"gemini-2.0-flash": {
		InputPerMillion:  0.10,
		OutputPerMillion: 0.40,
		ContextWindow:    1048576,
		MaxTokens:        8192,
		Vision:           true,
	},
	"gemini-2.0-flash-lite": {
		InputPerMillion:  0.075,
		OutputPerMillion: 0.30,
		ContextWindow:    1048576,
		MaxTokens:        8192,
	},
}

// MiniMaxDefaults holds pricing for MiniMax models accessed via the MiniMax Anthropic-compatible API.
// Applied for providers with type "anthropic" whose base_url targets api.minimax.io.
// Values are USD per million tokens (standard tier) as of 2026-03-21.
var MiniMaxDefaults = map[string]Entry{
	"MiniMax-M2.7": {
		InputPerMillion:      0.30,
		OutputPerMillion:     1.20,
		CacheWritePerMillion: 0.375,
		CacheReadPerMillion:  0.06,
		ContextWindow:        200000,
		MaxTokens:            128000,
		Reasoning:            true,
	},
	"MiniMax-M2.5": {
		InputPerMillion:      0.30,
		OutputPerMillion:     1.20,
		CacheWritePerMillion: 0.375,
		CacheReadPerMillion:  0.03,
		ContextWindow:        187000,
		MaxTokens:            32768,
	},
	"MiniMax-M2.1": {
		InputPerMillion:      0.30,
		OutputPerMillion:     1.20,
		CacheWritePerMillion: 0.375,
		CacheReadPerMillion:  0.03,
		ContextWindow:        192000,
		MaxTokens:            32768,
	},
	"MiniMax-M2": {
		InputPerMillion:      0.30,
		OutputPerMillion:     1.20,
		CacheWritePerMillion: 0.375,
		CacheReadPerMillion:  0.03,
		ContextWindow:        196000,
		MaxTokens:            16384,
	},
}

const (
	officialOpenAIHost      = "api.openai.com"
	officialChatGPTHost     = "chatgpt.com"
	officialCodexPathPrefix = "/backend-api/codex"
	chutesHost              = "llm.chutes.ai"
	syntheticHost           = "api.synthetic.new"
	minimaxHost             = "api.minimax.io"
)

// IsOfficialOpenAIURL reports whether baseURL targets an official OpenAI-priced endpoint.
// This includes the public API host and the ChatGPT Codex backend path.
func IsOfficialOpenAIURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	switch parsed.Hostname() {
	case officialOpenAIHost:
		return true
	case officialChatGPTHost:
		return strings.HasPrefix(strings.TrimRight(parsed.Path, "/"), officialCodexPathPrefix)
	default:
		return false
	}
}

const officialAnthropicHost = "api.anthropic.com"

// IsOfficialAnthropicURL reports whether baseURL targets the official Anthropic API.
func IsOfficialAnthropicURL(baseURL string) bool {
	return hostnameOf(baseURL) == officialAnthropicHost
}

// IsChutesURL reports whether baseURL targets the Chutes AI inference platform.
func IsChutesURL(baseURL string) bool {
	return hostnameOf(baseURL) == chutesHost
}

// IsSyntheticURL reports whether baseURL targets the Synthetic.new platform.
func IsSyntheticURL(baseURL string) bool {
	return hostnameOf(baseURL) == syntheticHost
}

// IsMiniMaxURL reports whether baseURL targets the MiniMax API platform.
func IsMiniMaxURL(baseURL string) bool {
	return hostnameOf(baseURL) == minimaxHost
}

// ProviderKey returns the canonical provider identifier used as the key prefix
// in the pricing calculator: "{type}:{hostname}".
// baseURL should be the provider's configured base URL (or the canonical default
// for providers whose URL is not user-configurable, such as GitHub Copilot).
func ProviderKey(providerType, baseURL string) string {
	host := hostnameOf(baseURL)
	if host == "" {
		return providerType
	}
	return providerType + ":" + host
}

// hostnameOf parses u and returns its hostname, or "" on error or empty input.
func hostnameOf(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

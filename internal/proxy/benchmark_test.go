package proxy_test

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/translate"
)

// BenchmarkRedactRequestBody benchmarks the JSON body redaction function.
func BenchmarkRedactRequestBody(b *testing.B) {
	bodies := map[string][]byte{
		"small_no_sensitive": []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`),
		"with_api_key":       []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"api_key":"sk-secret-12345","max_tokens":100}`),
		"nested_headers": func() []byte {
			data := map[string]interface{}{
				"model":      "claude-sonnet",
				"max_tokens": 100,
				"messages":   []map[string]string{{"role": "user", "content": "Hello world"}},
				"headers": map[string]string{
					"x-api-key":     "secret-key-value",
					"authorization": "Bearer sk-secret",
				},
			}
			b, _ := json.Marshal(data)
			return b
		}(),
		"large_body": func() []byte {
			messages := make([]map[string]string, 50)
			for i := range messages {
				messages[i] = map[string]string{
					"role":    "user",
					"content": "This is a reasonably long message that simulates a real conversation turn with enough content to be realistic in a benchmark scenario.",
				}
			}
			data := map[string]interface{}{
				"model":      "claude-sonnet",
				"max_tokens": 4096,
				"messages":   messages,
				"api_key":    "should-be-redacted",
			}
			b, _ := json.Marshal(data)
			return b
		}(),
	}

	for name, body := range bodies {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = proxy.RedactRequestBody(body)
			}
		})
	}
}

// BenchmarkOpenAIToAnthropic benchmarks the OpenAI-to-Anthropic request translation.
func BenchmarkOpenAIToAnthropic(b *testing.B) {
	maxTokens := 4096
	temp := 0.7

	cases := map[string]*translate.ChatCompletionRequest{
		"simple_message": {
			Model: "claude-sonnet-4-20250514",
			Messages: []translate.ChatMessage{
				{Role: "user", Content: "Hello, Claude!"},
			},
			MaxTokens: &maxTokens,
		},
		"with_system": {
			Model: "claude-sonnet-4-20250514",
			Messages: []translate.ChatMessage{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hello"},
			},
			MaxTokens:   &maxTokens,
			Temperature: &temp,
		},
		"multi_turn": {
			Model: "claude-sonnet-4-20250514",
			Messages: []translate.ChatMessage{
				{Role: "system", Content: "You are a coding assistant."},
				{Role: "user", Content: "Write a function to sort a list."},
				{Role: "assistant", Content: "Here is a sorting function in Python:\n\ndef sort_list(lst):\n    return sorted(lst)"},
				{Role: "user", Content: "Can you make it sort in reverse?"},
				{Role: "assistant", Content: "Sure:\n\ndef sort_list(lst, reverse=True):\n    return sorted(lst, reverse=reverse)"},
				{Role: "user", Content: "Now add type hints."},
			},
			MaxTokens: &maxTokens,
		},
		"with_tools": {
			Model: "claude-sonnet-4-20250514",
			Messages: []translate.ChatMessage{
				{Role: "user", Content: "What is the weather in NYC?"},
			},
			MaxTokens: &maxTokens,
			Tools: []translate.OpenAITool{
				{
					Type: "function",
					Function: translate.ToolFunction{
						Name:        "get_weather",
						Description: "Get the current weather for a location",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"location": map[string]interface{}{
									"type":        "string",
									"description": "The city name",
								},
							},
							"required": []string{"location"},
						},
					},
				},
			},
		},
	}

	for name, req := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := translate.OpenAIToAnthropic(req)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAnthropicToOpenAI benchmarks the Anthropic-to-OpenAI response translation.
func BenchmarkAnthropicToOpenAI(b *testing.B) {
	endTurn := "end_turn"
	toolUse := "tool_use"

	cases := map[string]*anthropic.MessagesResponse{
		"simple_text": {
			ID:   "msg_bench_1",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "Hello! How can I help you today?"},
			},
			Model:      "claude-sonnet-4-20250514",
			StopReason: &endTurn,
			Usage: anthropic.Usage{
				InputTokens:  50,
				OutputTokens: 25,
			},
		},
		"long_response": {
			ID:   "msg_bench_2",
			Type: "message",
			Role: "assistant",
			Content: func() []anthropic.ContentBlock {
				blocks := make([]anthropic.ContentBlock, 10)
				for i := range blocks {
					blocks[i] = anthropic.ContentBlock{
						Type: "text",
						Text: "This is a longer paragraph of text that simulates a real response from the model with enough content to be meaningful in a benchmark. ",
					}
				}
				return blocks
			}(),
			Model:      "claude-sonnet-4-20250514",
			StopReason: &endTurn,
			Usage: anthropic.Usage{
				InputTokens:  500,
				OutputTokens: 1200,
			},
		},
		"with_tool_call": {
			ID:   "msg_bench_3",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "Let me check the weather for you."},
				{
					Type:  "tool_use",
					ID:    "toolu_bench_1",
					Name:  "get_weather",
					Input: map[string]interface{}{"location": "New York City", "units": "fahrenheit"},
				},
			},
			Model:      "claude-sonnet-4-20250514",
			StopReason: &toolUse,
			Usage: anthropic.Usage{
				InputTokens:  200,
				OutputTokens: 80,
			},
		},
	}

	for name, resp := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = translate.AnthropicToOpenAI(resp, "claude-sonnet")
			}
		})
	}
}

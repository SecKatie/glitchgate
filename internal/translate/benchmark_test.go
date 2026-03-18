// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// ---------------------------------------------------------------------------
// Helper builders
// ---------------------------------------------------------------------------

func benchResponsesRequest() *openai.ResponsesRequest {
	temp := 0.7
	maxTokens := 4096
	instructions := "You are a helpful coding assistant."
	return &openai.ResponsesRequest{
		Model:           "gpt-4o",
		Input:           json.RawMessage(`[{"type":"message","role":"user","content":"Explain the difference between concurrency and parallelism in Go, with code examples."},{"type":"message","role":"assistant","content":"Concurrency is about dealing with lots of things at once..."},{"type":"message","role":"user","content":"Can you show a practical example using worker pools?"}]`),
		Instructions:    &instructions,
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
		Tools: []openai.ResponsesTool{
			{
				Type:        "function",
				Name:        "run_code",
				Description: "Execute a Go code snippet and return stdout/stderr.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"Go source code to execute"},"timeout":{"type":"integer","description":"Timeout in seconds"}},"required":["code"]}`),
			},
		},
	}
}

func benchAnthropicRequest() *anthropic.MessagesRequest {
	temp := 0.7
	return &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096,
		System:    "You are a helpful coding assistant.",
		Messages: []anthropic.Message{
			{Role: "user", Content: "Explain the difference between concurrency and parallelism in Go, with code examples."},
			{Role: "assistant", Content: "Concurrency is about dealing with lots of things at once..."},
			{Role: "user", Content: "Can you show a practical example using worker pools?"},
		},
		Temperature: &temp,
		Tools: []anthropic.Tool{
			{
				Name:        "run_code",
				Description: "Execute a Go code snippet and return stdout/stderr.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"code":    map[string]interface{}{"type": "string", "description": "Go source code to execute"},
						"timeout": map[string]interface{}{"type": "integer", "description": "Timeout in seconds"},
					},
					"required": []string{"code"},
				},
			},
		},
	}
}

func benchChatCompletionRequest() *openai.ChatCompletionRequest {
	temp := 0.7
	maxTokens := 4096
	return &openai.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []openai.ChatMessage{
			{Role: "system", Content: "You are a helpful coding assistant."},
			{Role: "user", Content: "Explain the difference between concurrency and parallelism in Go, with code examples."},
			{Role: "assistant", Content: "Concurrency is about dealing with lots of things at once..."},
			{Role: "user", Content: "Can you show a practical example using worker pools?"},
		},
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Tools: []openai.OpenAITool{
			{
				Type: "function",
				Function: openai.ToolFunction{
					Name:        "run_code",
					Description: "Execute a Go code snippet and return stdout/stderr.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"code":    map[string]interface{}{"type": "string", "description": "Go source code to execute"},
							"timeout": map[string]interface{}{"type": "integer", "description": "Timeout in seconds"},
						},
						"required": []string{"code"},
					},
				},
			},
		},
	}
}

func benchAnthropicResponseBody() []byte {
	stopReason := "end_turn"
	resp := anthropic.MessagesResponse{
		ID:   "msg_01XFDUDYJgAACzvnptvVoYEL",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{
				Type: "text",
				Text: "Here's a practical worker pool example in Go:\n\n```go\npackage main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc worker(id int, jobs <-chan int, results chan<- int, wg *sync.WaitGroup) {\n\tdefer wg.Done()\n\tfor j := range jobs {\n\t\tfmt.Printf(\"Worker %d processing job %d\\n\", id, j)\n\t\tresults <- j * 2\n\t}\n}\n\nfunc main() {\n\tconst numJobs = 10\n\tconst numWorkers = 3\n\tjobs := make(chan int, numJobs)\n\tresults := make(chan int, numJobs)\n\tvar wg sync.WaitGroup\n\tfor w := 1; w <= numWorkers; w++ {\n\t\twg.Add(1)\n\t\tgo worker(w, jobs, results, &wg)\n\t}\n\tfor j := 1; j <= numJobs; j++ {\n\t\tjobs <- j\n\t}\n\tclose(jobs)\n\twg.Wait()\n\tclose(results)\n\tfor r := range results {\n\t\tfmt.Println(r)\n\t}\n}\n```\n\nThis demonstrates concurrency with goroutines coordinated via channels and a WaitGroup.",
			},
		},
		Model:      "claude-sonnet-4-20250514",
		StopReason: &stopReason,
		Usage: anthropic.Usage{
			InputTokens:  245,
			OutputTokens: 312,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func benchOpenAIResponseBody() []byte {
	finishReason := "stop"
	resp := openai.ChatCompletionResponse{
		ID:      "chatcmpl-abc123def456",
		Object:  "chat.completion",
		Created: 1710288000,
		Model:   "gpt-4o-2024-05-13",
		Choices: []openai.Choice{
			{
				Index: 0,
				Message: &openai.ChatMessage{
					Role:    "assistant",
					Content: "Here's a practical worker pool example in Go:\n\n```go\npackage main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc worker(id int, jobs <-chan int, results chan<- int, wg *sync.WaitGroup) {\n\tdefer wg.Done()\n\tfor j := range jobs {\n\t\tfmt.Printf(\"Worker %d processing job %d\\n\", id, j)\n\t\tresults <- j * 2\n\t}\n}\n\nfunc main() {\n\tconst numJobs = 10\n\tconst numWorkers = 3\n\tjobs := make(chan int, numJobs)\n\tresults := make(chan int, numJobs)\n\tvar wg sync.WaitGroup\n\tfor w := 1; w <= numWorkers; w++ {\n\t\twg.Add(1)\n\t\tgo worker(w, jobs, results, &wg)\n\t}\n\tfor j := 1; j <= numJobs; j++ {\n\t\tjobs <- j\n\t}\n\tclose(jobs)\n\twg.Wait()\n\tclose(results)\n\tfor r := range results {\n\t\tfmt.Println(r)\n\t}\n}\n```",
				},
				FinishReason: &finishReason,
			},
		},
		Usage: &openai.OpenAIUsage{
			PromptTokens:     245,
			CompletionTokens: 312,
			TotalTokens:      557,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func benchResponsesResponseBody() []byte {
	resp := openai.ResponsesResponse{
		ID:        "resp_01XFDUDYJgAACzvnptvVoYEL",
		Object:    "response",
		CreatedAt: 1710288000,
		Model:     "gpt-4o-2024-05-13",
		Status:    "completed",
		Output: []openai.OutputItem{
			{
				Type:   "message",
				ID:     "msg_01XFDUDYJgAACzvnptvVoYEL",
				Role:   "assistant",
				Status: "completed",
				Content: []openai.OutputContent{
					{
						Type:        "output_text",
						Text:        "Here's a practical worker pool example in Go:\n\n```go\npackage main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc worker(id int, jobs <-chan int, results chan<- int, wg *sync.WaitGroup) {\n\tdefer wg.Done()\n\tfor j := range jobs {\n\t\tfmt.Printf(\"Worker %d processing job %d\\n\", id, j)\n\t\tresults <- j * 2\n\t}\n}\n\nfunc main() {\n\tconst numJobs = 10\n\tconst numWorkers = 3\n\tjobs := make(chan int, numJobs)\n\tresults := make(chan int, numJobs)\n\tvar wg sync.WaitGroup\n\tfor w := 1; w <= numWorkers; w++ {\n\t\twg.Add(1)\n\t\tgo worker(w, jobs, results, &wg)\n\t}\n\tfor j := 1; j <= numJobs; j++ {\n\t\tjobs <- j\n\t}\n\tclose(jobs)\n\twg.Wait()\n\tclose(results)\n\tfor r := range results {\n\t\tfmt.Println(r)\n\t}\n}\n```",
						Annotations: []any{},
					},
				},
			},
		},
		Usage: &openai.ResponsesUsage{
			InputTokens:  245,
			OutputTokens: 312,
			TotalTokens:  557,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// ---------------------------------------------------------------------------
// Request translation benchmarks
// ---------------------------------------------------------------------------

func BenchmarkResponsesToAnthropic(b *testing.B) {
	b.ReportAllocs()
	req := benchResponsesRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	}
}

func BenchmarkResponsesToOpenAI(b *testing.B) {
	b.ReportAllocs()
	req := benchResponsesRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ResponsesToOpenAI(req, "gpt-4o")
	}
}

func BenchmarkAnthropicToResponses(b *testing.B) {
	b.ReportAllocs()
	req := benchAnthropicRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = AnthropicToResponses(req, "gpt-4o")
	}
}

func BenchmarkOpenAIToResponses(b *testing.B) {
	b.ReportAllocs()
	req := benchChatCompletionRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = OpenAIToResponses(req, "gpt-4o")
	}
}

// ---------------------------------------------------------------------------
// Response translation benchmarks
// ---------------------------------------------------------------------------

func BenchmarkAnthropicToResponsesResponse(b *testing.B) {
	b.ReportAllocs()
	body := benchAnthropicResponseBody()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AnthropicToResponsesResponse(body, "claude-sonnet-4-20250514")
	}
}

func BenchmarkOpenAIToResponsesResponse(b *testing.B) {
	b.ReportAllocs()
	body := benchOpenAIResponseBody()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = OpenAIToResponsesResponse(body, "gpt-4o")
	}
}

func BenchmarkResponsesToAnthropicResponse(b *testing.B) {
	b.ReportAllocs()
	body := benchResponsesResponseBody()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ResponsesToAnthropicResponse(body, "claude-sonnet-4-20250514")
	}
}

func BenchmarkResponsesToOpenAIResponse(b *testing.B) {
	b.ReportAllocs()
	body := benchResponsesResponseBody()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ResponsesToOpenAIResponse(body, "gpt-4o")
	}
}

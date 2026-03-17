// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConversation(t *testing.T) {
	t.Run("empty request body sets ParseFailed", func(t *testing.T) {
		cd := parseConversation("", "")
		require.True(t, cd.ParseFailed)
		require.Nil(t, cd.LatestPrompt)
		require.Nil(t, cd.Response)
		require.Empty(t, cd.History)
	})

	t.Run("invalid JSON sets ParseFailed", func(t *testing.T) {
		cd := parseConversation("not-json", "")
		require.True(t, cd.ParseFailed)
		require.Equal(t, "not-json", cd.RawRequest)
	})

	t.Run("empty messages array sets ParseFailed", func(t *testing.T) {
		cd := parseConversation(`{"model":"claude-test","messages":[]}`, "")
		require.True(t, cd.ParseFailed)
	})

	t.Run("single user turn — no history", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hello"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "user", cd.LatestPrompt.Role)
		require.Len(t, cd.LatestPrompt.Blocks, 1)
		require.Equal(t, "Hello", cd.LatestPrompt.Blocks[0].Text)
		require.Empty(t, cd.History)
	})

	t.Run("multi-turn conversation splits history and latest prompt", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[
			{"role":"user","content":"First question"},
			{"role":"assistant","content":"First answer"},
			{"role":"user","content":"Second question"}
		]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "Second question", cd.LatestPrompt.Blocks[0].Text)
		// History contains the two earlier turns.
		require.Len(t, cd.History, 2)
		require.Equal(t, "user", cd.History[0].Role)
		require.Equal(t, "assistant", cd.History[1].Role)
	})

	t.Run("string system prompt is normalised", func(t *testing.T) {
		req := `{"model":"claude-test","system":"Be helpful.","messages":[{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.True(t, cd.HasSystem)
		require.Equal(t, "Be helpful.", cd.SystemPrompt)
	})

	t.Run("array system prompt is joined", func(t *testing.T) {
		req := `{"model":"claude-test","system":[{"type":"text","text":"Part one."},{"type":"text","text":"Part two."}],"messages":[{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.True(t, cd.HasSystem)
		require.Contains(t, cd.SystemPrompt, "Part one.")
		require.Contains(t, cd.SystemPrompt, "Part two.")
	})

	t.Run("no system prompt leaves HasSystem false", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.HasSystem)
		require.Empty(t, cd.SystemPrompt)
	})

	t.Run("response body is parsed into Response turn", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hi"}]}`
		resp := `{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"text","text":"Hello there!"}]}`
		cd := parseConversation(req, resp)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Equal(t, "assistant", cd.Response.Role)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "Hello there!", cd.Response.Blocks[0].Text)
	})

	t.Run("invalid response body leaves Response nil", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "not-json")
		require.Nil(t, cd.Response)
	})

	t.Run("tool use block is parsed", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[
			{"role":"user","content":"What is the weather?"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"get_weather","input":{"location":"NYC"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"Sunny, 72°F"}]}
		]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		// The latest user message contains the tool result.
		require.Len(t, cd.LatestPrompt.Blocks, 1)
		require.Equal(t, "tool_result", cd.LatestPrompt.Blocks[0].Type)
		// The assistant turn is in history.
		require.Len(t, cd.History, 2)
		assistantTurn := cd.History[1]
		require.Equal(t, "assistant", assistantTurn.Role)
		require.Len(t, assistantTurn.Blocks, 1)
		require.Equal(t, "tool_use", assistantTurn.Blocks[0].Type)
		require.Equal(t, "get_weather", assistantTurn.Blocks[0].ToolName)
	})

	t.Run("long text is truncated with FullText preserved", func(t *testing.T) {
		// Use newlines to test line-based truncation (3 lines)
		// JSON-escaped newlines since we're building a JSON string
		longText := "line1\\nline2\\nline3\\nline4\\nline5"
		req := `{"model":"claude-test","messages":[{"role":"user","content":"` + longText + `"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.True(t, cd.LatestPrompt.Blocks[0].Truncated)
		require.Equal(t, "line1\nline2\nline3", cd.LatestPrompt.Blocks[0].Text)
		require.Equal(t, "line1\nline2\nline3\nline4\nline5", cd.LatestPrompt.Blocks[0].FullText)
	})

	t.Run("raw bodies are always populated even on parse failure", func(t *testing.T) {
		cd := parseConversation(`{"invalid":true}`, `{"also":"invalid"}`)
		require.NotEmpty(t, cd.RawRequest)
		require.NotEmpty(t, cd.RawResponse)
	})

	t.Run("pretty-printed raw JSON is indented", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.Contains(t, cd.RawRequest, "\n")
	})

	t.Run("SSE streaming response body is parsed into Response turn", func(t *testing.T) {
		req := `{"model":"claude-test","messages":[{"role":"user","content":"Hi"}]}`
		sseBody := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello from stream\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		cd := parseConversation(req, sseBody)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Equal(t, "assistant", cd.Response.Role)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "Hello from stream", cd.Response.Blocks[0].Text)
	})

	t.Run("responses request string input populates system prompt and latest prompt", func(t *testing.T) {
		req := `{"model":"gpt-5.4","instructions":"Be brief.","input":"Hello from responses"}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.True(t, cd.HasSystem)
		require.Equal(t, "Be brief.", cd.SystemPrompt)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "user", cd.LatestPrompt.Role)
		require.Equal(t, "Hello from responses", cd.LatestPrompt.Blocks[0].Text)
	})

	t.Run("responses request message items keep developer history and latest user prompt", func(t *testing.T) {
		req := `{
			"model":"gpt-5.4",
			"instructions":"Top level instructions",
			"input":[
				{"type":"message","role":"developer","content":[{"type":"input_text","text":"Developer guardrails"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"Show me the details"}]}
			]
		}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.Equal(t, "Top level instructions", cd.SystemPrompt)
		require.Len(t, cd.History, 1)
		require.Equal(t, "system", cd.History[0].Role)
		require.Equal(t, "Developer guardrails", cd.History[0].Blocks[0].Text)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "Show me the details", cd.LatestPrompt.Blocks[0].Text)
	})

	t.Run("responses function call and output are rendered as tool turns", func(t *testing.T) {
		req := `{
			"model":"gpt-5.4",
			"input":[
				{"type":"function_call","call_id":"call_123","name":"get_weather","arguments":"{\"location\":\"SF\"}"},
				{"type":"function_call_output","call_id":"call_123","output":"{\"temp\":70}"}
			]
		}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.Len(t, cd.History, 1)
		require.Equal(t, "assistant", cd.History[0].Role)
		require.Equal(t, "tool_use", cd.History[0].Blocks[0].Type)
		require.Equal(t, "get_weather", cd.History[0].Blocks[0].ToolName)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "tool_result", cd.LatestPrompt.Blocks[0].Type)
		require.Equal(t, "get_weather", cd.LatestPrompt.Blocks[0].ToolName)
	})

	t.Run("responses response body is parsed into assistant text", func(t *testing.T) {
		req := `{"model":"gpt-5.4","input":"Hi"}`
		resp := `{
			"id":"resp_123",
			"object":"response",
			"status":"completed",
			"output":[
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello from Responses"}]}
			]
		}`
		cd := parseConversation(req, resp)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Equal(t, "assistant", cd.Response.Role)
		require.Equal(t, "Hello from Responses", cd.Response.Blocks[0].Text)
	})

	t.Run("responses response function call is parsed into tool use", func(t *testing.T) {
		req := `{"model":"gpt-5.4","input":"Hi"}`
		resp := `{
			"id":"resp_456",
			"object":"response",
			"status":"completed",
			"output":[
				{"type":"function_call","call_id":"call_456","name":"lookup_weather","arguments":"{\"city\":\"Boston\"}","status":"completed"}
			]
		}`
		cd := parseConversation(req, resp)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "tool_use", cd.Response.Blocks[0].Type)
		require.Equal(t, "lookup_weather", cd.Response.Blocks[0].ToolName)
	})

	t.Run("responses SSE delta stream is parsed into assistant text", func(t *testing.T) {
		req := `{"model":"gpt-5.4","input":"Hi"}`
		sseBody := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream1\",\"model\":\"gpt-5.4\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello from responses stream\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":50}}}\n\n" +
			"data: [DONE]\n\n"
		cd := parseConversation(req, sseBody)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Equal(t, "assistant", cd.Response.Role)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "Hello from responses stream", cd.Response.Blocks[0].Text)
	})

	t.Run("responses unsupported items remain visible", func(t *testing.T) {
		req := `{
			"model":"gpt-5.4",
			"input":[
				{"type":"item_reference","id":"msg_123"},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"Real prompt"}]}
			]
		}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.Len(t, cd.History, 1)
		require.Equal(t, "unknown", cd.History[0].Blocks[0].Type)
		require.Contains(t, cd.History[0].Blocks[0].Text, "item_reference")
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "Real prompt", cd.LatestPrompt.Blocks[0].Text)
	})

	// OpenAI Chat Completions format tests
	t.Run("openai simple user message is parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.LatestPrompt)
		require.Equal(t, "user", cd.LatestPrompt.Role)
		require.Len(t, cd.LatestPrompt.Blocks, 1)
		require.Equal(t, "Hello", cd.LatestPrompt.Blocks[0].Text)
	})

	t.Run("openai system message is parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.True(t, cd.HasSystem)
		require.Equal(t, "Be helpful", cd.SystemPrompt)
		require.Equal(t, "Hi", cd.LatestPrompt.Blocks[0].Text)
	})

	t.Run("openai developer replaces system role", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"developer","content":"Developer prompt"},{"role":"user","content":"Hi"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.True(t, cd.HasSystem)
		require.Equal(t, "Developer prompt", cd.SystemPrompt)
	})

	t.Run("openai multi-turn conversation builds history", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[
			{"role":"user","content":"First question"},
			{"role":"assistant","content":"First answer"},
			{"role":"user","content":"Second question"}
		]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.Len(t, cd.History, 2)
		require.Equal(t, "user", cd.History[0].Role)
		require.Equal(t, "assistant", cd.History[1].Role)
		require.Equal(t, "Second question", cd.LatestPrompt.Blocks[0].Text)
	})

	t.Run("openai non-streaming response is parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}]}`
		resp := `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello there!"},"finish_reason":"stop"}]}`
		cd := parseConversation(req, resp)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Equal(t, "assistant", cd.Response.Role)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "Hello there!", cd.Response.Blocks[0].Text)
	})

	t.Run("openai streaming response is parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}]}`
		sseBody := "data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello from\"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" stream\"}}]}\n\n" +
			"data: [DONE]\n\n"
		cd := parseConversation(req, sseBody)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "Hello from stream", cd.Response.Blocks[0].Text)
	})

	t.Run("openai tool use and tool results are parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[
			{"role":"user","content":"What is the weather?"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"NYC\"}"}}]},
			{"role":"tool","tool_call_id":"call_123","content":"Sunny, 72°F"}
		]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		// History: user question + assistant with tool call (2 items)
		require.Len(t, cd.History, 2)
		require.Equal(t, "user", cd.History[0].Role)
		require.Equal(t, "assistant", cd.History[1].Role)
		// Latest prompt: tool result
		require.NotNil(t, cd.LatestPrompt)
		require.Len(t, cd.LatestPrompt.Blocks, 1)
		require.Equal(t, "tool_result", cd.LatestPrompt.Blocks[0].Type)
		require.Equal(t, "get_weather", cd.LatestPrompt.Blocks[0].ToolName)
	})

	t.Run("openai streaming tool calls are parsed", func(t *testing.T) {
		req := `{"model":"gpt-4","messages":[{"role":"user","content":"Get the weather"}]}`
		sseBody := "data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null,\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"Boston\\\"}\"}}]}}]}\n\n" +
			"data: [DONE]\n\n"
		cd := parseConversation(req, sseBody)
		require.False(t, cd.ParseFailed)
		require.NotNil(t, cd.Response)
		require.Len(t, cd.Response.Blocks, 1)
		require.Equal(t, "tool_use", cd.Response.Blocks[0].Type)
		require.Equal(t, "get_weather", cd.Response.Blocks[0].ToolName)
	})

	t.Run("openai image content shows image label", func(t *testing.T) {
		req := `{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,abc123"}}]}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.Len(t, cd.LatestPrompt.Blocks, 1)
		require.Equal(t, "image", cd.LatestPrompt.Blocks[0].Type)
		require.Contains(t, cd.LatestPrompt.Blocks[0].MediaLabel, "image")
	})
}

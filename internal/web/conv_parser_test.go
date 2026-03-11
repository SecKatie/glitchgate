// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
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
		longText := strings.Repeat("a", 600)
		req := `{"model":"claude-test","messages":[{"role":"user","content":"` + longText + `"}]}`
		cd := parseConversation(req, "")
		require.False(t, cd.ParseFailed)
		require.True(t, cd.LatestPrompt.Blocks[0].Truncated)
		require.Equal(t, 500, len([]rune(cd.LatestPrompt.Blocks[0].Text)))
		require.Equal(t, longText, cd.LatestPrompt.Blocks[0].FullText)
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
}

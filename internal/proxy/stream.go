package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// maxSSELineSize is the maximum size of a single SSE line we support.
// Anthropic's extended thinking produces signature_delta events that encode
// the full thinking content as a base64 signature, which can easily exceed
// bufio.Scanner's default 64 KB limit. 1 MB handles realistic thinking sizes.
const maxSSELineSize = 1 << 20 // 1 MB

// newSSEScanner creates a bufio.Scanner with a buffer large enough for
// extended thinking signature_delta events.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, maxSSELineSize), maxSSELineSize)
	return s
}

// StreamResult holds the captured data from a streamed response.
type StreamResult struct {
	Body                     []byte
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
}

// RelaySSEStream reads SSE events from upstream and forwards them to the client,
// flushing after each event. It captures the full stream and extracts token usage.
func RelaySSEStream(w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		captured.WriteString(line)
		captured.WriteByte('\n')

		// Extract token counts from specific SSE events.
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			extractTokens(data, &inputTokens, &outputTokens, &cacheCreationTokens, &cacheReadTokens)
		}

		// Write the line to the client.
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return &StreamResult{
				Body:                     captured.Bytes(),
				InputTokens:              inputTokens,
				OutputTokens:             outputTokens,
				CacheCreationInputTokens: cacheCreationTokens,
				CacheReadInputTokens:     cacheReadTokens,
			}, err
		}

		// Flush after blank lines (SSE event boundary).
		if line == "" {
			if err := rc.Flush(); err != nil {
				return &StreamResult{
					Body:                     captured.Bytes(),
					InputTokens:              inputTokens,
					OutputTokens:             outputTokens,
					CacheCreationInputTokens: cacheCreationTokens,
					CacheReadInputTokens:     cacheReadTokens,
				}, err
			}
		}
	}

	return &StreamResult{
		Body:                     captured.Bytes(),
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
	}, scanner.Err()
}

// RelayOpenAISSEStream reads OpenAI-format SSE events from upstream and forwards
// them to the client, flushing after each event. It captures the full stream and
// extracts token usage from the final chunk's usage field.
func RelayOpenAISSEStream(w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens int64

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		captured.WriteString(line)
		captured.WriteByte('\n')

		// Extract token counts from SSE data events.
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data != "[DONE]" {
				extractOpenAITokens(data, &inputTokens, &outputTokens)
			}
		}

		// Write the line to the client.
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return &StreamResult{
				Body:         captured.Bytes(),
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			}, err
		}

		// Flush after blank lines (SSE event boundary).
		if line == "" {
			if err := rc.Flush(); err != nil {
				return &StreamResult{
					Body:         captured.Bytes(),
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				}, err
			}
		}
	}

	return &StreamResult{
		Body:         captured.Bytes(),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, scanner.Err()
}

// RelayResponsesSSEStream reads Responses API SSE events from upstream and forwards
// them to the client, flushing after each event. It captures the full stream and
// extracts token usage from the response.completed event.
func RelayResponsesSSEStream(w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		captured.WriteString(line)
		captured.WriteByte('\n')

		// Extract token counts from response.completed events.
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data != "[DONE]" {
				extractResponsesTokens(data, &inputTokens, &outputTokens, &cacheReadTokens, &reasoningTokens)
			}
		}

		// Write the line to the client.
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return &StreamResult{
				Body:                 captured.Bytes(),
				InputTokens:          inputTokens,
				OutputTokens:         outputTokens,
				CacheReadInputTokens: cacheReadTokens,
				ReasoningTokens:      reasoningTokens,
			}, err
		}

		// Flush after blank lines (SSE event boundary).
		if line == "" {
			if err := rc.Flush(); err != nil {
				return &StreamResult{
					Body:                 captured.Bytes(),
					InputTokens:          inputTokens,
					OutputTokens:         outputTokens,
					CacheReadInputTokens: cacheReadTokens,
					ReasoningTokens:      reasoningTokens,
				}, err
			}
		}
	}

	return &StreamResult{
		Body:                 captured.Bytes(),
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CacheReadInputTokens: cacheReadTokens,
		ReasoningTokens:      reasoningTokens,
	}, scanner.Err()
}

// SynthesizeAnthropicSSE writes a complete Anthropic MessagesResponse as
// Server-Sent Events to w. It is used when the upstream was called without
// streaming (stream: false on the provider) but the client expects SSE.
func SynthesizeAnthropicSSE(w http.ResponseWriter, resp *anthropic.MessagesResponse) (*StreamResult, error) {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer

	emit := func(eventType, data string) error {
		line := "event: " + eventType + "\ndata: " + data + "\n\n"
		captured.WriteString(line)
		if _, err := w.Write([]byte(line)); err != nil {
			return err
		}
		return rc.Flush()
	}

	// message_start — content is empty; usage shows input tokens only.
	msgStart := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            resp.ID,
			"type":          "message",
			"role":          "assistant",
			"model":         resp.Model,
			"content":       []interface{}{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int64{
				"input_tokens":                resp.Usage.InputTokens,
				"output_tokens":               0,
				"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
				"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
			},
		},
	}
	data, _ := json.Marshal(msgStart)
	if err := emit("message_start", string(data)); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	if err := emit("ping", `{"type":"ping"}`); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	// One SSE block per content block.
	for i, block := range resp.Content {
		// content_block_start
		var startBlock interface{}
		switch block.Type {
		case "tool_use":
			startBlock = map[string]interface{}{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]interface{}{
					"type":  "tool_use",
					"id":    block.ID,
					"name":  block.Name,
					"input": map[string]interface{}{},
				},
			}
		default: // "text" and anything else
			startBlock = map[string]interface{}{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]interface{}{
					"type": block.Type,
					"text": "",
				},
			}
		}
		data, _ = json.Marshal(startBlock)
		if err := emit("content_block_start", string(data)); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}

		// content_block_delta
		var delta anthropic.DeltaBlock
		switch block.Type {
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			delta = anthropic.DeltaBlock{Type: "input_json_delta", PartialJSON: string(inputJSON)}
		default:
			delta = anthropic.DeltaBlock{Type: "text_delta", Text: block.Text}
		}
		deltaEvt := anthropic.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: i,
			Delta: delta,
		}
		data, _ = json.Marshal(deltaEvt)
		if err := emit("content_block_delta", string(data)); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}

		// content_block_stop
		stopEvt := map[string]interface{}{"type": "content_block_stop", "index": i}
		data, _ = json.Marshal(stopEvt)
		if err := emit("content_block_stop", string(data)); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}
	}

	// message_delta — stop_reason and output token count.
	msgDelta := anthropic.MessageDeltaEvent{
		Type:  "message_delta",
		Delta: anthropic.MessageDelta{StopReason: resp.StopReason, StopSequence: resp.StopSequence},
		Usage: &anthropic.DeltaUsage{OutputTokens: resp.Usage.OutputTokens},
	}
	data, _ = json.Marshal(msgDelta)
	if err := emit("message_delta", string(data)); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	if err := emit("message_stop", `{"type":"message_stop"}`); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	return &StreamResult{
		Body:                     captured.Bytes(),
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
	}, nil
}

// from the response.completed event's usage field.
func extractResponsesTokens(data string, inputTokens, outputTokens, cacheReadTokens, reasoningTokens *int64) {
	if !strings.Contains(data, "response.completed") {
		return
	}

	var event struct {
		Type     string `json:"type"`
		Response *struct {
			Usage *struct {
				InputTokens        int64 `json:"input_tokens"`
				OutputTokens       int64 `json:"output_tokens"`
				InputTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails *struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}

	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}
	if event.Type == "response.completed" && event.Response != nil && event.Response.Usage != nil {
		*inputTokens = event.Response.Usage.InputTokens
		*outputTokens = event.Response.Usage.OutputTokens
		if event.Response.Usage.InputTokensDetails != nil {
			*cacheReadTokens = event.Response.Usage.InputTokensDetails.CachedTokens
			*inputTokens -= *cacheReadTokens
			if *inputTokens < 0 {
				*inputTokens = 0
			}
		}
		if event.Response.Usage.OutputTokensDetails != nil {
			*reasoningTokens = event.Response.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
}

// extractOpenAITokens parses an OpenAI SSE data payload for token usage.
// OpenAI includes usage in a final chunk with a "usage" field.
func extractOpenAITokens(data string, inputTokens, outputTokens *int64) {
	if !strings.Contains(data, "\"usage\"") {
		return
	}

	var chunk struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.Usage != nil {
		*inputTokens = chunk.Usage.PromptTokens
		*outputTokens = chunk.Usage.CompletionTokens
	}
}

// extractTokens parses SSE data payloads for token usage information.
// message_start contains input_tokens and cache token counts; message_delta contains output_tokens.
func extractTokens(data string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens *int64) {
	// Quick type check to avoid unnecessary JSON parsing.
	if !strings.Contains(data, "message_start") && !strings.Contains(data, "message_delta") {
		return
	}

	var envelope struct {
		Type    string `json:"type"`
		Message *struct {
			Usage *anthropic.Usage `json:"usage"`
		} `json:"message"`
		Usage *anthropic.DeltaUsage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return
	}

	switch envelope.Type {
	case "message_start":
		if envelope.Message != nil && envelope.Message.Usage != nil {
			*inputTokens = envelope.Message.Usage.InputTokens
			*cacheCreationTokens = envelope.Message.Usage.CacheCreationInputTokens
			*cacheReadTokens = envelope.Message.Usage.CacheReadInputTokens
		}
	case "message_delta":
		if envelope.Usage != nil {
			*outputTokens = envelope.Usage.OutputTokens
		}
	}
}

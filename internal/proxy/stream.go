package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/sse"
	"github.com/seckatie/glitchgate/internal/translate"
)

// StreamResult is a type alias for sse.StreamResult, preserving backward
// compatibility for callers within the proxy package.
type StreamResult = sse.StreamResult

// streamingResult converts a StreamResult and error from a stream relay or
// translation function into a handlerResult. This consolidates the identical
// pattern repeated across all streaming handler methods.
func streamingResult(resp *provider.Response, result *sse.StreamResult, err error) handlerResult {
	var errDetails *string
	if err != nil {
		errDetails = streamRelayErrorDetails(err)
		if errDetails != nil {
			slog.Warn("stream relay error", "error", err)
		}
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	return handlerResult{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheCreationInputTokens: result.CacheCreationInputTokens,
		CacheReadInputTokens:     result.CacheReadInputTokens,
		ReasoningTokens:          result.ReasoningTokens,
		Status:                   status,
		Body:                     result.Body,
		ErrDetails:               errDetails,
		IsStreaming:              true,
	}
}

// RelaySSEStream reads SSE events from upstream and forwards them to the client,
// flushing after each event. It captures the full stream and extracts token usage.
// When ctx is cancelled (e.g. client disconnect), the upstream reader is closed
// to unblock the scanner and stop consuming provider resources.
func RelaySSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

	scanner := sse.NewScanner(upstream)
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
func RelayOpenAISSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		captured.WriteString(line)
		captured.WriteByte('\n')

		// Extract token counts from SSE data events.
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data != "[DONE]" {
				extractOpenAITokens(data, &inputTokens, &outputTokens, &cacheReadTokens, &reasoningTokens)
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

// RelayResponsesSSEStream reads Responses API SSE events from upstream and forwards
// them to the client, flushing after each event. It captures the full stream and
// extracts token usage from the response.completed event.
func RelayResponsesSSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64

	scanner := sse.NewScanner(upstream)
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

	sse.WriteHeaders(w)

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
	data, err := json.Marshal(msgStart)
	if err != nil {
		return &StreamResult{}, fmt.Errorf("marshalling message_start: %w", err)
	}
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
		data, err := json.Marshal(startBlock)
		if err != nil {
			return &StreamResult{Body: captured.Bytes()}, fmt.Errorf("marshalling content_block_start: %w", err)
		}
		if err := emit("content_block_start", string(data)); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}

		// content_block_delta
		var delta anthropic.DeltaBlock
		switch block.Type {
		case "tool_use":
			inputJSON, err := json.Marshal(block.Input)
			if err != nil {
				return &StreamResult{Body: captured.Bytes()}, fmt.Errorf("marshalling tool input: %w", err)
			}
			delta = anthropic.DeltaBlock{Type: "input_json_delta", PartialJSON: string(inputJSON)}
		default:
			delta = anthropic.DeltaBlock{Type: "text_delta", Text: block.Text}
		}
		deltaEvt := anthropic.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: i,
			Delta: delta,
		}
		data, err = json.Marshal(deltaEvt)
		if err != nil {
			return &StreamResult{Body: captured.Bytes()}, fmt.Errorf("marshalling content_block_delta: %w", err)
		}
		if err := emit("content_block_delta", string(data)); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}

		// content_block_stop
		stopEvt := map[string]interface{}{"type": "content_block_stop", "index": i}
		data, err = json.Marshal(stopEvt)
		if err != nil {
			return &StreamResult{Body: captured.Bytes()}, fmt.Errorf("marshalling content_block_stop: %w", err)
		}
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
	data, err = json.Marshal(msgDelta)
	if err != nil {
		return &StreamResult{Body: captured.Bytes()}, fmt.Errorf("marshalling message_delta: %w", err)
	}
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

// SynthesizeOpenAISSE writes a complete Chat Completions response as
// Server-Sent Events to w. It is used when the upstream was called without
// streaming but the client expects OpenAI-format SSE.
func SynthesizeOpenAISSE(w http.ResponseWriter, resp *translate.ChatCompletionResponse) (*StreamResult, error) {
	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer

	writeChunk := func(chunk *translate.ChatCompletionResponse) error {
		data, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		line := "data: " + string(data) + "\n\n"
		captured.WriteString(line)
		if _, err := w.Write([]byte(line)); err != nil {
			return err
		}
		return rc.Flush()
	}

	if resp == nil {
		resp = &translate.ChatCompletionResponse{
			ID:      "chatcmpl-gemini",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
		}
	}

	created := resp.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := resp.Model
	if model == "" {
		model = "unknown"
	}
	id := resp.ID
	if id == "" {
		id = "chatcmpl-gemini"
	}

	var message *translate.ChatMessage
	var finishReason *string
	if len(resp.Choices) > 0 {
		message = resp.Choices[0].Message
		finishReason = resp.Choices[0].FinishReason
	}
	if message == nil {
		message = &translate.ChatMessage{Role: "assistant"}
	}

	initial := &translate.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []translate.Choice{{
			Index: 0,
			Delta: &translate.ChatMessage{Role: "assistant"},
		}},
	}
	if err := writeChunk(initial); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	if text, ok := message.Content.(string); ok && text != "" {
		if err := writeChunk(&translate.ChatCompletionResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []translate.Choice{{
				Index: 0,
				Delta: &translate.ChatMessage{Content: text},
			}},
		}); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}
	}

	for _, tc := range message.ToolCalls {
		if err := writeChunk(&translate.ChatCompletionResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []translate.Choice{{
				Index: 0,
				Delta: &translate.ChatMessage{
					ToolCalls: []translate.ToolCall{tc},
				},
			}},
		}); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}
	}

	if err := writeChunk(&translate.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []translate.Choice{{
			Index:        0,
			Delta:        &translate.ChatMessage{},
			FinishReason: finishReason,
		}},
	}); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	done := "data: [DONE]\n\n"
	captured.WriteString(done)
	if _, err := w.Write([]byte(done)); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}
	if err := rc.Flush(); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	if resp.Usage != nil {
		inputTokens = resp.Usage.PromptTokens
		outputTokens = resp.Usage.CompletionTokens
		if resp.Usage.PromptTokensDetails != nil {
			cacheReadTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
		if resp.Usage.CompletionTokensDetails != nil {
			reasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}
	return &StreamResult{
		Body:                 captured.Bytes(),
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CacheReadInputTokens: cacheReadTokens,
		ReasoningTokens:      reasoningTokens,
	}, nil
}

// SynthesizeResponsesSSE writes a complete Responses API response as
// Server-Sent Events to w. It is used when the upstream was called without
// streaming but the client expects Responses-format SSE.
func SynthesizeResponsesSSE(w http.ResponseWriter, resp *translate.ResponsesResponse) (*StreamResult, error) {
	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer

	writeEvent := func(event string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
		captured.WriteString(line)
		if _, err := w.Write([]byte(line)); err != nil {
			return err
		}
		return rc.Flush()
	}

	if resp == nil {
		resp = &translate.ResponsesResponse{
			ID:     "resp_gemini",
			Object: "response",
			Status: "completed",
		}
	}

	respID := resp.ID
	if respID == "" {
		respID = "resp_gemini"
	}
	model := resp.Model
	if model == "" {
		model = "unknown"
	}

	if err := writeEvent("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     respID,
			"object": "response",
			"status": "in_progress",
			"model":  model,
		},
	}); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	for outputIndex, item := range resp.Output {
		switch item.Type {
		case "message":
			if err := writeEvent("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": outputIndex,
				"item": map[string]any{
					"id":      item.ID,
					"type":    "message",
					"status":  "in_progress",
					"role":    item.Role,
					"content": []any{},
				},
			}); err != nil {
				return &StreamResult{Body: captured.Bytes()}, err
			}
			for contentIndex, content := range item.Content {
				if content.Type != "output_text" {
					continue
				}
				if err := writeEvent("response.content_part.added", map[string]any{
					"type":          "response.content_part.added",
					"item_id":       item.ID,
					"output_index":  outputIndex,
					"content_index": contentIndex,
					"part":          map[string]any{"type": "output_text", "text": ""},
				}); err != nil {
					return &StreamResult{Body: captured.Bytes()}, err
				}
				if err := writeEvent("response.output_text.delta", map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       item.ID,
					"output_index":  outputIndex,
					"content_index": contentIndex,
					"delta":         content.Text,
				}); err != nil {
					return &StreamResult{Body: captured.Bytes()}, err
				}
				if err := writeEvent("response.output_text.done", map[string]any{
					"type":          "response.output_text.done",
					"item_id":       item.ID,
					"output_index":  outputIndex,
					"content_index": contentIndex,
					"text":          content.Text,
				}); err != nil {
					return &StreamResult{Body: captured.Bytes()}, err
				}
				if err := writeEvent("response.content_part.done", map[string]any{
					"type":          "response.content_part.done",
					"item_id":       item.ID,
					"output_index":  outputIndex,
					"content_index": contentIndex,
					"part":          map[string]any{"type": "output_text", "text": content.Text},
				}); err != nil {
					return &StreamResult{Body: captured.Bytes()}, err
				}
			}
			if err := writeEvent("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": outputIndex,
				"item":         item,
			}); err != nil {
				return &StreamResult{Body: captured.Bytes()}, err
			}
		case "function_call":
			if err := writeEvent("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": outputIndex,
				"item":         item,
			}); err != nil {
				return &StreamResult{Body: captured.Bytes()}, err
			}
			if err := writeEvent("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": outputIndex,
				"item":         item,
			}); err != nil {
				return &StreamResult{Body: captured.Bytes()}, err
			}
		}
	}

	if err := writeEvent("response.completed", map[string]any{
		"type":     "response.completed",
		"response": resp,
	}); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	done := "data: [DONE]\n\n"
	captured.WriteString(done)
	if _, err := w.Write([]byte(done)); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}
	if err := rc.Flush(); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	if resp.Usage != nil {
		inputTokens = resp.Usage.InputTokens
		outputTokens = resp.Usage.OutputTokens
		if resp.Usage.InputTokensDetails != nil {
			cacheReadTokens = resp.Usage.InputTokensDetails.CachedTokens
		}
		if resp.Usage.OutputTokensDetails != nil {
			reasoningTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
	return &StreamResult{
		Body:                 captured.Bytes(),
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CacheReadInputTokens: cacheReadTokens,
		ReasoningTokens:      reasoningTokens,
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
// completion_tokens already includes reasoning_tokens (sub-breakdown, not additive).
// cached_tokens is a sub-breakdown of prompt_tokens and is subtracted to get net input.
func extractOpenAITokens(data string, inputTokens, outputTokens, cacheReadTokens, reasoningTokens *int64) {
	if !strings.Contains(data, "\"usage\"") {
		return
	}

	var chunk struct {
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			ReasoningTokens     int64 `json:"reasoning_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.Usage != nil {
		*inputTokens = chunk.Usage.PromptTokens
		*outputTokens = chunk.Usage.CompletionTokens
		if chunk.Usage.PromptTokensDetails != nil {
			*cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			*inputTokens -= *cacheReadTokens
			if *inputTokens < 0 {
				*inputTokens = 0
			}
		}
		if chunk.Usage.CompletionTokensDetails != nil {
			*reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if *reasoningTokens == 0 && chunk.Usage.ReasoningTokens > 0 {
			*reasoningTokens = chunk.Usage.ReasoningTokens
		}
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
			// message_delta may also include updated cache token counts and input_tokens.
			if envelope.Usage.InputTokens > 0 {
				*inputTokens = envelope.Usage.InputTokens
			}
			if envelope.Usage.CacheCreationInputTokens > 0 {
				*cacheCreationTokens = envelope.Usage.CacheCreationInputTokens
			}
			if envelope.Usage.CacheReadInputTokens > 0 {
				*cacheReadTokens = envelope.Usage.CacheReadInputTokens
			}
		}
	}
}

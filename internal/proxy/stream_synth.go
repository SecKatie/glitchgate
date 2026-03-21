package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/sse"
)

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
	msgStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            resp.ID,
			"type":          "message",
			"role":          "assistant",
			"model":         resp.Model,
			"content":       []any{},
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
		var startBlock any
		switch block.Type {
		case "tool_use":
			startBlock = map[string]any{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    block.ID,
					"name":  block.Name,
					"input": map[string]any{},
				},
			}
		default: // "text" and anything else
			startBlock = map[string]any{
				"type":  "content_block_start",
				"index": i,
				"content_block": map[string]any{
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
		stopEvt := map[string]any{"type": "content_block_stop", "index": i}
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
func SynthesizeOpenAISSE(w http.ResponseWriter, resp *openai.ChatCompletionResponse) (*StreamResult, error) {
	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer

	writeChunk := func(chunk *openai.ChatCompletionResponse) error {
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
		resp = &openai.ChatCompletionResponse{
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

	var message *openai.ChatMessage
	var finishReason *string
	if len(resp.Choices) > 0 {
		message = resp.Choices[0].Message
		finishReason = resp.Choices[0].FinishReason
	}
	if message == nil {
		message = &openai.ChatMessage{Role: "assistant"}
	}

	initial := &openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{{
			Index: 0,
			Delta: &openai.ChatMessage{Role: "assistant"},
		}},
	}
	if err := writeChunk(initial); err != nil {
		return &StreamResult{Body: captured.Bytes()}, err
	}

	if text, ok := message.Content.(string); ok && text != "" {
		if err := writeChunk(&openai.ChatCompletionResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openai.Choice{{
				Index: 0,
				Delta: &openai.ChatMessage{Content: text},
			}},
		}); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}
	}

	for _, tc := range message.ToolCalls {
		if err := writeChunk(&openai.ChatCompletionResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []openai.Choice{{
				Index: 0,
				Delta: &openai.ChatMessage{
					ToolCalls: []openai.ToolCall{tc},
				},
			}},
		}); err != nil {
			return &StreamResult{Body: captured.Bytes()}, err
		}
	}

	if err := writeChunk(&openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{{
			Index:        0,
			Delta:        &openai.ChatMessage{},
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
func SynthesizeResponsesSSE(w http.ResponseWriter, resp *openai.ResponsesResponse) (*StreamResult, error) {
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
		resp = &openai.ResponsesResponse{
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

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

// StreamResult holds the captured data from a streamed response.
type StreamResult struct {
	Body                     []byte
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
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

	scanner := bufio.NewScanner(upstream)
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

	scanner := bufio.NewScanner(upstream)
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

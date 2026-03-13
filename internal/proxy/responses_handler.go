// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
	"codeberg.org/kglitchy/glitchgate/internal/store"
	"codeberg.org/kglitchy/glitchgate/internal/translate"
)

// ResponsesHandler is the proxy HTTP handler for Responses API requests.
type ResponsesHandler struct {
	cfg        *config.Config
	providers  map[string]provider.Provider
	calculator *pricing.Calculator
	logger     *AsyncLogger
}

// NewResponsesHandler creates a new Responses API proxy handler.
func NewResponsesHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger) *ResponsesHandler {
	return &ResponsesHandler{
		cfg:        cfg,
		providers:  providers,
		calculator: calc,
		logger:     logger,
	}
}

// ServeHTTP handles POST /v1/responses requests.
func (h *ResponsesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeResponsesError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	start := time.Now()

	// Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	// Parse the Responses API request.
	var req translate.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}

	if req.Model == "" {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Missing required field: model")
		return
	}

	// Validate input field: must be a string or valid JSON array.
	if len(req.Input) > 0 {
		firstByte := req.Input[0]
		if firstByte != '"' && firstByte != '[' {
			writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Input must be a string or array of input items")
			return
		}
	}

	// Validate temperature range.
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Temperature must be between 0 and 2")
		return
	}

	// Validate tool names are unique.
	if len(req.Tools) > 0 {
		seen := make(map[string]bool, len(req.Tools))
		for _, t := range req.Tools {
			if t.Name != "" {
				if seen[t.Name] {
					writeResponsesError(w, http.StatusBadRequest, "invalid_request_error",
						fmt.Sprintf("Duplicate tool name: %s", t.Name))
					return
				}
				seen[t.Name] = true
			}
		}
	}

	isStreaming := req.Stream != nil && *req.Stream

	logResponsesCacheDebug(body, &req)

	// Resolve model chain.
	chain, err := h.cfg.FindModel(req.Model)
	if err != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model: %s", req.Model))
		return
	}

	// Get the authenticated proxy key for logging.
	pk := KeyFromContext(r.Context())
	proxyKeyID := ""
	if pk != nil {
		proxyKeyID = pk.ID
	}

	// Iterate the fallback chain.
	for attempt, mapping := range chain {
		attemptCount := int64(attempt + 1)

		// Find the provider for this chain entry.
		prov, ok := h.providers[mapping.Provider]
		if !ok {
			writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", mapping.Provider))
			return
		}

		provKey := provKeyFor(h.cfg, prov)

		var (
			upstreamBody []byte
			provResp     *provider.Response
		)

		// Route based on upstream provider format.
		switch prov.APIFormat() {
		case "responses":
			var bodyMap map[string]any
			if err := json.Unmarshal(body, &bodyMap); err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
				return
			}
			bodyMap["model"] = mapping.UpstreamModel
			upstreamBody, err = json.Marshal(bodyMap)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return
			}
		case "anthropic":
			var anthReq any
			anthReq, err = translate.ResponsesToAnthropic(&req, mapping.UpstreamModel)
			if err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
				return
			}
			upstreamBody, err = json.Marshal(anthReq)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return
			}
		case "openai":
			var ccReq any
			ccReq, err = translate.ResponsesToOpenAI(&req, mapping.UpstreamModel)
			if err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
				return
			}
			upstreamBody, err = json.Marshal(ccReq)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return
			}
		default:
			writeResponsesError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("Unsupported upstream format %q for provider %s", prov.APIFormat(), prov.Name()))
			return
		}

		provReq := &provider.Request{
			Body:        upstreamBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: isStreaming,
		}

		provResp, err = prov.SendRequest(r.Context(), provReq)
		if err != nil {
			if attempt < len(chain)-1 {
				continue
			}
			latency := time.Since(start).Milliseconds()
			errMsg := err.Error()
			h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
				0, 0, 0, 0, 0, latency, http.StatusBadGateway, upstreamBody, []byte(errMsg), &errMsg, isStreaming, attemptCount)
			writeResponsesError(w, http.StatusBadGateway, "server_error", "Failed to reach upstream provider")
			return
		}

		if isFallbackStatus(provResp.StatusCode) {
			if provResp.Stream != nil {
				_ = provResp.Stream.Close()
			}
			if attempt < len(chain)-1 {
				continue
			}
			latency := time.Since(start).Milliseconds()
			errMsg := fmt.Sprintf("all %d fallback entries exhausted; last status %d", attemptCount, provResp.StatusCode)
			h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
				0, 0, 0, 0, 0, latency, http.StatusServiceUnavailable, upstreamBody, []byte(errMsg), &errMsg, isStreaming, attemptCount)
			writeResponsesError(w, http.StatusServiceUnavailable, "server_error", "All upstream providers failed")
			return
		}

		switch prov.APIFormat() {
		case "responses":
			if isStreaming {
				h.handleResponsesStreaming(w, provResp, proxyKeyID, provKey, req.Model, mapping.UpstreamModel, upstreamBody, start, attemptCount)
			} else {
				h.handleResponsesNonStreaming(w, provResp, proxyKeyID, provKey, req.Model, mapping.UpstreamModel, upstreamBody, start, attemptCount)
			}
		case "anthropic":
			h.handleAnthropicProviderResponse(w, provResp, body, &req, &mapping, proxyKeyID, provKey, start, attemptCount, isStreaming)
		case "openai":
			h.handleOpenAICCProviderResponse(w, provResp, body, &req, &mapping, proxyKeyID, provKey, start, attemptCount, isStreaming)
		}
		return
	}
}

type responsesCacheDebugFields struct {
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention"`
}

func logResponsesCacheDebug(body []byte, req *translate.ResponsesRequest) {
	var raw responsesCacheDebugFields
	if err := json.Unmarshal(body, &raw); err != nil {
		slog.Debug("responses handler: cache fields", "parse_error", err, "body_bytes", len(body))
		return
	}

	instructionsBytes := 0
	instructionsHash := ""
	if req.Instructions != nil {
		instructionsBytes = len(*req.Instructions)
		instructionsHash = shortHash([]byte(*req.Instructions))
	}

	slog.Debug("responses handler: cache fields",
		"body_bytes", len(body),
		"input_bytes", len(req.Input),
		"input_prefix_hash", shortHashPrefix(req.Input, 4096),
		"input_hash", shortHash(req.Input),
		"instructions_bytes", instructionsBytes,
		"instructions_hash", instructionsHash,
		"tools_count", len(req.Tools),
		"tools_hash", shortHashJSON(req.Tools),
		"previous_response_id_present", req.PreviousResponseID != nil && *req.PreviousResponseID != "",
		"previous_response_id_hash", shortStringPtrHash(req.PreviousResponseID),
		"prompt_cache_key_present", len(raw.PromptCacheKey) > 0 && string(raw.PromptCacheKey) != "null",
		"prompt_cache_key_hash", shortHash(raw.PromptCacheKey),
		"prompt_cache_retention", compactJSON(raw.PromptCacheRetention, 32),
		"store", compactBoolPtr(req.Store),
		"truncation", compactStringPtr(req.Truncation),
		"metadata_keys", len(req.Metadata),
	)
}

func shortHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:6])
}

func shortHashPrefix(data []byte, n int) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) < n {
		n = len(data)
	}
	return shortHash(data[:n])
}

func shortHashJSON(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "marshal_err"
	}
	return shortHash(data)
}

func shortStringPtrHash(v *string) string {
	if v == nil || *v == "" {
		return ""
	}
	return shortHash([]byte(*v))
}

func compactJSON(raw json.RawMessage, maxLength int) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	s := string(raw)
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "..."
}

func compactBoolPtr(v *bool) string {
	if v == nil {
		return ""
	}
	if *v {
		return "true"
	}
	return "false"
}

func compactStringPtr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// handleResponsesNonStreaming forwards a non-streaming Responses API response.
func (h *ResponsesHandler) handleResponsesNonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		errDetails = &s
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(resp.Body); err != nil {
			slog.Warn("write Responses error response", "error", err)
		}
		h.logResponsesRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
			resp.InputTokens, resp.OutputTokens, 0, resp.CacheReadInputTokens, resp.ReasoningTokens, latency, resp.StatusCode,
			reqBody, resp.Body, errDetails, false, attemptCount)
		return
	}

	// Forward response as-is.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(resp.Body); err != nil {
		slog.Warn("write Responses response", "error", err)
	}

	h.logResponsesRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		resp.InputTokens, resp.OutputTokens, 0, resp.CacheReadInputTokens, resp.ReasoningTokens, latency, http.StatusOK,
		reqBody, resp.Body, nil, false, attemptCount)
}

// handleResponsesStreaming relays a Responses API SSE stream.
func (h *ResponsesHandler) handleResponsesStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	result, err := RelayResponsesSSEStream(w, resp.Stream)
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream relay error: %v", err)
		errDetails = &s
		slog.Warn("stream relay error", "error", err)
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	h.logResponsesRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, result.CacheCreationInputTokens, result.CacheReadInputTokens,
		result.ReasoningTokens, latency, status, reqBody, result.Body, errDetails, true, attemptCount)
}

func (h *ResponsesHandler) handleAnthropicProviderResponse(w http.ResponseWriter, provResp *provider.Response,
	body []byte, req *translate.ResponsesRequest, mapping *config.ModelMapping,
	proxyKeyID, provKey string, start time.Time, attemptCount int64, isStreaming bool,
) {
	if isStreaming {
		// Translate Anthropic SSE stream to Responses API SSE stream.
		result, err := translate.AnthropicSSEToResponsesSSE(w, provResp.Stream, req.Model)
		latency := time.Since(start).Milliseconds()

		var errDetails *string
		if err != nil {
			s := fmt.Sprintf("stream relay error: %v", err)
			errDetails = &s
			slog.Warn("stream relay error", "error", err)
		}

		h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
			result.InputTokens, result.OutputTokens, result.CacheCreationInputTokens, result.CacheReadInputTokens,
			result.ReasoningTokens, latency, http.StatusOK, body, result.Body, errDetails, true, attemptCount)
		return
	}

	// Non-streaming: translate Anthropic response to Responses API format.
	if provResp.StatusCode >= 400 {
		latency := time.Since(start).Milliseconds()
		s := string(provResp.Body)
		h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
			0, 0, 0, 0, 0, latency, provResp.StatusCode, body, provResp.Body, &s, false, attemptCount)
		writeResponsesError(w, provResp.StatusCode, "server_error", s)
		return
	}

	var anthResp struct {
		ID         string          `json:"id"`
		Content    json.RawMessage `json:"content"`
		StopReason *string         `json:"stop_reason"`
		Usage      struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(provResp.Body, &anthResp); err != nil {
		writeResponsesError(w, http.StatusBadGateway, "server_error", "Failed to parse upstream response")
		return
	}

	responsesResp := translate.AnthropicToResponsesResponse(provResp.Body, req.Model)
	respBody, err := json.Marshal(responsesResp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		return
	}

	latency := time.Since(start).Milliseconds()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		slog.Warn("write Responses response", "error", err)
	}

	h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
		anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens,
		anthResp.Usage.CacheCreationInputTokens, anthResp.Usage.CacheReadInputTokens,
		0, latency, http.StatusOK, body, respBody, nil, false, attemptCount)
}

func (h *ResponsesHandler) handleOpenAICCProviderResponse(w http.ResponseWriter, provResp *provider.Response,
	body []byte, req *translate.ResponsesRequest, mapping *config.ModelMapping,
	proxyKeyID, provKey string, start time.Time, attemptCount int64, isStreaming bool,
) {
	if isStreaming {
		// Translate OpenAI SSE stream to Responses API SSE stream.
		result, err := translate.OpenAISSEToResponsesSSE(w, provResp.Stream, req.Model)
		latency := time.Since(start).Milliseconds()

		var errDetails *string
		if err != nil {
			s := fmt.Sprintf("stream relay error: %v", err)
			errDetails = &s
			slog.Warn("stream relay error", "error", err)
		}

		h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
			result.InputTokens, result.OutputTokens, 0, 0,
			result.ReasoningTokens, latency, http.StatusOK, body, result.Body, errDetails, true, attemptCount)
		return
	}

	// Non-streaming: translate Chat Completions response to Responses API format.
	if provResp.StatusCode >= 400 {
		latency := time.Since(start).Milliseconds()
		s := string(provResp.Body)
		h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
			0, 0, 0, 0, 0, latency, provResp.StatusCode, body, provResp.Body, &s, false, attemptCount)
		writeResponsesError(w, provResp.StatusCode, "server_error", s)
		return
	}

	responsesResp := translate.OpenAIToResponsesResponse(provResp.Body, req.Model)
	respBody, err := json.Marshal(responsesResp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		return
	}

	latency := time.Since(start).Milliseconds()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		slog.Warn("write Responses response", "error", err)
	}

	h.logResponsesRequest(proxyKeyID, provKey, req.Model, mapping.UpstreamModel,
		provResp.InputTokens, provResp.OutputTokens, 0, 0,
		provResp.ReasoningTokens, latency, http.StatusOK, body, respBody, nil, false, attemptCount)
}

func (h *ResponsesHandler) logResponsesRequest(proxyKeyID, providerName, modelRequested, modelUpstream string,
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens, latencyMs int64, status int,
	requestBody, responseBody []byte, errDetails *string, isStreaming bool, attemptCount int64,
) {
	entry := &store.RequestLogEntry{
		ID:                       uuid.New().String(),
		ProxyKeyID:               proxyKeyID,
		Timestamp:                time.Now().UTC(),
		SourceFormat:             "responses",
		ProviderName:             providerName,
		ModelRequested:           modelRequested,
		ModelUpstream:            modelUpstream,
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ReasoningTokens:          reasoningTokens,
		LatencyMs:                latencyMs,
		Status:                   status,
		RequestBody:              RedactRequestBody(requestBody),
		ResponseBody:             string(responseBody),
		ErrorDetails:             errDetails,
		IsStreaming:              isStreaming,
		FallbackAttempts:         attemptCount,
	}

	h.logger.Log(entry)
}

// writeResponsesError writes an error response in Responses API format.
func writeResponsesError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := translate.ResponsesResponse{
		Object: "response",
		Status: "failed",
		Error: &translate.ResponsesError{
			Code:    code,
			Message: message,
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("write Responses error", "error", err)
	}
}

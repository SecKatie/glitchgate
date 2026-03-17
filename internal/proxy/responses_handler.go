// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/metrics"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/translate"
)

// ResponsesHandler is the proxy HTTP handler for Responses API requests.
type ResponsesHandler struct {
	cfg           *config.Config
	providers     map[string]provider.Provider
	calculator    *pricing.Calculator
	logger        *AsyncLogger
	budgetChecker *BudgetChecker
}

// NewResponsesHandler creates a new Responses API proxy handler.
func NewResponsesHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger, bc *BudgetChecker) *ResponsesHandler {
	return &ResponsesHandler{
		cfg:           cfg,
		providers:     providers,
		calculator:    calc,
		logger:        logger,
		budgetChecker: bc,
	}
}

// ServeHTTP handles POST /v1/responses requests.
func (h *ResponsesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeResponsesError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	start := time.Now()

	body, ok := readRequestBodyWithLimit(w, r, h.cfg.ProxyMaxBodyBytes, "invalid_request_error", writeResponsesError)
	if !ok {
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
	keyPrefix := ""
	if pk != nil {
		proxyKeyID = pk.ID
		keyPrefix = pk.KeyPrefix
	}

	metrics.RecordActiveRequest("responses")
	defer metrics.FinishActiveRequest("responses")

	if h.budgetChecker != nil && proxyKeyID != "" {
		if violation, err := h.budgetChecker.Check(r.Context(), proxyKeyID); err != nil {
			slog.Warn("budget check error", "error", err)
		} else if violation != nil {
			writeResponsesError(w, http.StatusTooManyRequests, "budget_exceeded",
				fmt.Sprintf("Budget exceeded: %s %s limit $%.2f, spent $%.2f, resets %s",
					violation.Scope, violation.Period, violation.LimitUSD, violation.SpendUSD,
					violation.ResetAt.In(h.budgetChecker.tz).Format(time.RFC3339)))
			return
		}
	}

	executeProxyPipeline(w, r, h.logger, chain, h.providers, pipelineSpec{
		SourceFormat: "responses",
		ProxyKeyID:   proxyKeyID,
		KeyPrefix:    keyPrefix,
		ModelRequest: req.Model,
		IsStreaming:  isStreaming,
		Start:        start,
		Calculator:   h.calculator,
	}, h.routeBuilders(w, r, body, &req, isStreaming),
		newPipelineCallbacks(w, writeResponsesError, "server_error"))
}

func (h *ResponsesHandler) routeBuilders(
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	req *translate.ResponsesRequest,
	isStreaming bool,
) map[string]routeBuilder {
	return map[string]routeBuilder{
		"responses": func(attempt chainAttempt) (*routePlan, bool) {
			mapping := attempt.Mapping
			var bodyMap map[string]any
			if err := json.Unmarshal(body, &bodyMap); err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
				return nil, true
			}
			bodyMap["model"] = mapping.UpstreamModel
			upstreamBody, err := json.Marshal(bodyMap)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return nil, true
			}
			return &routePlan{
				ProviderRequest: &provider.Request{
					Body:        upstreamBody,
					Headers:     r.Header.Clone(),
					Model:       mapping.UpstreamModel,
					IsStreaming: isStreaming,
				},
				RequestBody: body,
				HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
					if isStreaming {
						return h.handleResponsesStreaming(r.Context(), w, provResp)
					}
					return h.handleResponsesNonStreaming(w, provResp)
				},
			}, false
		},
		"anthropic": func(attempt chainAttempt) (*routePlan, bool) {
			mapping := attempt.Mapping
			anthReq, err := translate.ResponsesToAnthropic(req, mapping.UpstreamModel)
			if err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
				return nil, true
			}
			upstreamBody, err := json.Marshal(anthReq)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return nil, true
			}
			return &routePlan{
				ProviderRequest: &provider.Request{
					Body:        upstreamBody,
					Headers:     r.Header.Clone(),
					Model:       mapping.UpstreamModel,
					IsStreaming: isStreaming,
				},
				RequestBody: body,
				HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
					if isStreaming {
						return h.handleAnthropicProviderStreaming(w, provResp, req.Model)
					}
					return h.handleAnthropicProviderNonStreaming(w, provResp, req.Model)
				},
			}, false
		},
		"openai": func(attempt chainAttempt) (*routePlan, bool) {
			mapping := attempt.Mapping
			ccReq, err := translate.ResponsesToOpenAI(req, mapping.UpstreamModel)
			if err != nil {
				writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
				return nil, true
			}
			upstreamBody, err := json.Marshal(ccReq)
			if err != nil {
				writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
				return nil, true
			}
			return &routePlan{
				ProviderRequest: &provider.Request{
					Body:        upstreamBody,
					Headers:     r.Header.Clone(),
					Model:       mapping.UpstreamModel,
					IsStreaming: isStreaming,
				},
				RequestBody: body,
				HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
					if isStreaming {
						return h.handleOpenAICCProviderStreaming(w, provResp, req.Model)
					}
					return h.handleOpenAICCProviderNonStreaming(w, provResp, req.Model)
				},
			}, false
		},
		"gemini": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildGeminiRoute(w, r, req, body, isStreaming, &attempt.Mapping)
		},
	}
}

type responsesCacheDebugFields struct {
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention"`
}

func logResponsesCacheDebug(body []byte, req *translate.ResponsesRequest) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
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
func (h *ResponsesHandler) handleResponsesNonStreaming(w http.ResponseWriter, resp *provider.Response) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(resp.Body); err != nil {
			slog.Warn("write Responses error response", "error", err)
		}
		return handlerResult{
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			CacheReadInputTokens: resp.CacheReadInputTokens,
			ReasoningTokens:      resp.ReasoningTokens,
			Status:               resp.StatusCode,
			Body:                 resp.Body,
			ErrDetails:           &s,
		}
	}

	// Forward response as-is.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(resp.Body); err != nil {
		slog.Warn("write Responses response", "error", err)
	}
	return handlerResult{
		InputTokens:          resp.InputTokens,
		OutputTokens:         resp.OutputTokens,
		CacheReadInputTokens: resp.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 resp.Body,
	}
}

// handleResponsesStreaming relays a Responses API SSE stream.
func (h *ResponsesHandler) handleResponsesStreaming(ctx context.Context, w http.ResponseWriter, resp *provider.Response) handlerResult {
	result, err := RelayResponsesSSEStream(ctx, w, resp.Stream)

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

func (h *ResponsesHandler) handleAnthropicProviderNonStreaming(w http.ResponseWriter, provResp *provider.Response, modelRequested string) handlerResult {
	if provResp.StatusCode >= 400 {
		s := string(provResp.Body)
		writeResponsesError(w, provResp.StatusCode, "server_error", s)
		return handlerResult{
			Status:     provResp.StatusCode,
			Body:       provResp.Body,
			ErrDetails: &s,
		}
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
		s := "Failed to parse upstream response"
		return handlerResult{Status: http.StatusBadGateway, Body: provResp.Body, ErrDetails: &s}
	}

	responsesResp := translate.AnthropicToResponsesResponse(provResp.Body, modelRequested)
	respBody, err := json.Marshal(responsesResp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		slog.Warn("write Responses response", "error", err)
	}
	return handlerResult{
		InputTokens:              anthResp.Usage.InputTokens,
		OutputTokens:             anthResp.Usage.OutputTokens,
		CacheCreationInputTokens: anthResp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     anthResp.Usage.CacheReadInputTokens,
		Status:                   http.StatusOK,
		Body:                     respBody,
	}
}

func (h *ResponsesHandler) handleAnthropicProviderStreaming(w http.ResponseWriter, provResp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.AnthropicSSEToResponsesSSE(w, provResp.Stream, modelRequested)

	var errDetails *string
	if err != nil {
		errDetails = streamRelayErrorDetails(err)
		if errDetails != nil {
			slog.Warn("stream relay error", "error", err)
		}
	}

	return handlerResult{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheCreationInputTokens: result.CacheCreationInputTokens,
		CacheReadInputTokens:     result.CacheReadInputTokens,
		ReasoningTokens:          result.ReasoningTokens,
		Status:                   http.StatusOK,
		Body:                     result.Body,
		ErrDetails:               errDetails,
		IsStreaming:              true,
	}
}

func (h *ResponsesHandler) handleOpenAICCProviderNonStreaming(w http.ResponseWriter, provResp *provider.Response, modelRequested string) handlerResult {
	if provResp.StatusCode >= 400 {
		s := string(provResp.Body)
		writeResponsesError(w, provResp.StatusCode, "server_error", s)
		return handlerResult{
			Status:     provResp.StatusCode,
			Body:       provResp.Body,
			ErrDetails: &s,
		}
	}

	responsesResp := translate.OpenAIToResponsesResponse(provResp.Body, modelRequested)
	respBody, err := json.Marshal(responsesResp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		slog.Warn("write Responses response", "error", err)
	}
	return handlerResult{
		InputTokens:     provResp.InputTokens,
		OutputTokens:    provResp.OutputTokens,
		ReasoningTokens: provResp.ReasoningTokens,
		Status:          http.StatusOK,
		Body:            respBody,
	}
}

func (h *ResponsesHandler) handleOpenAICCProviderStreaming(w http.ResponseWriter, provResp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.OpenAISSEToResponsesSSE(w, provResp.Stream, modelRequested)

	var errDetails *string
	if err != nil {
		errDetails = streamRelayErrorDetails(err)
		if errDetails != nil {
			slog.Warn("stream relay error", "error", err)
		}
	}

	return handlerResult{
		InputTokens:     result.InputTokens,
		OutputTokens:    result.OutputTokens,
		ReasoningTokens: result.ReasoningTokens,
		Status:          http.StatusOK,
		Body:            result.Body,
		ErrDetails:      errDetails,
		IsStreaming:     true,
	}
}

// buildGeminiRoute handles Responses API requests that need to be sent to a
// native Gemini (Vertex AI) provider. Translates Responses→Gemini on request
// and Gemini→Responses on response.
func (h *ResponsesHandler) buildGeminiRoute(
	w http.ResponseWriter, r *http.Request,
	req *translate.ResponsesRequest, rawBody []byte, isStreaming bool,
	mapping *config.DispatchTarget,
) (*routePlan, bool) {
	gemReq, err := translate.ResponsesToGeminiRequest(req)
	if err != nil {
		writeResponsesError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	gemBody, err := json.Marshal(gemReq)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		return nil, true
	}

	forceNonStream := false
	if provCfg, cfgErr := h.cfg.FindProvider(mapping.Provider); cfgErr == nil && provCfg.Stream != nil && !*provCfg.Stream {
		forceNonStream = true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        gemBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: isStreaming && !forceNonStream,
		},
		RequestBody: rawBody,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			switch {
			case isStreaming && forceNonStream:
				return h.handleGeminiForcedStreamToResponses(w, provResp, req.Model)
			case isStreaming:
				return h.handleGeminiStreamingToResponses(w, provResp, req.Model)
			default:
				return h.handleGeminiNonStreamingToResponses(w, provResp, req.Model)
			}
		},
	}, false
}

func (h *ResponsesHandler) handleGeminiNonStreamingToResponses(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeResponsesError(w, resp.StatusCode, "server_error", s)
		return handlerResult{
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			CacheReadInputTokens: resp.CacheReadInputTokens,
			ReasoningTokens:      resp.ReasoningTokens,
			Status:               resp.StatusCode,
			Body:                 resp.Body,
			ErrDetails:           &s,
		}
	}

	respResp := translate.GeminiToResponsesResponse(resp.Body, modelRequested)

	respBody, err := json.Marshal(respResp)
	if err != nil {
		writeResponsesError(w, http.StatusInternalServerError, "server_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		slog.Warn("write Responses response", "error", err)
	}
	return handlerResult{
		InputTokens:          resp.InputTokens,
		OutputTokens:         resp.OutputTokens,
		CacheReadInputTokens: resp.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 respBody,
	}
}

func (h *ResponsesHandler) handleGeminiStreamingToResponses(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.Stream == nil {
		return h.handleGeminiForcedStreamToResponses(w, resp, modelRequested)
	}

	result, err := translate.GeminiSSEToResponsesSSE(w, resp.Stream, modelRequested)

	var errDetails *string
	if err != nil {
		errDetails = streamRelayErrorDetails(err)
		if errDetails != nil {
			slog.Warn("gemini stream relay error", "error", err)
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

func (h *ResponsesHandler) handleGeminiForcedStreamToResponses(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeResponsesError(w, resp.StatusCode, "server_error", s)
		return handlerResult{
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			CacheReadInputTokens: resp.CacheReadInputTokens,
			ReasoningTokens:      resp.ReasoningTokens,
			Status:               resp.StatusCode,
			Body:                 resp.Body,
			ErrDetails:           &s,
			IsStreaming:          true,
		}
	}

	respResp := translate.GeminiToResponsesResponse(resp.Body, modelRequested)
	result, err := SynthesizeResponsesSSE(w, respResp)

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream synthesis error: %v", err)
		errDetails = &s
		slog.Warn("stream synthesis error", "error", err)
	}

	return handlerResult{
		InputTokens:          resp.InputTokens,
		OutputTokens:         resp.OutputTokens,
		CacheReadInputTokens: resp.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 result.Body,
		ErrDetails:           errDetails,
		IsStreaming:          true,
	}
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

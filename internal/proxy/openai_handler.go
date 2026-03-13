package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
	anthropic "codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
	"codeberg.org/kglitchy/glitchgate/internal/translate"
)

// OpenAIHandler is the proxy HTTP handler for OpenAI-compatible requests.
// It translates OpenAI Chat Completions requests to Anthropic format,
// dispatches them via the configured provider, and translates responses
// back to OpenAI format.
type OpenAIHandler struct {
	cfg        *config.Config
	providers  map[string]provider.Provider
	calculator *pricing.Calculator
	logger     *AsyncLogger
}

// NewOpenAIHandler creates a new OpenAI-compatible proxy handler.
func NewOpenAIHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger) *OpenAIHandler {
	return &OpenAIHandler{
		cfg:        cfg,
		providers:  providers,
		calculator: calc,
		logger:     logger,
	}
}

// ServeHTTP handles POST /v1/chat/completions requests.
func (h *OpenAIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	start := time.Now()

	// Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	// Parse the OpenAI request.
	var oaiReq translate.ChatCompletionRequest
	if err := json.Unmarshal(body, &oaiReq); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}

	if oaiReq.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Missing required field: model")
		return
	}

	// Resolve model chain.
	chain, err := h.cfg.FindModel(oaiReq.Model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model: %s", oaiReq.Model))
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
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", mapping.Provider))
			return
		}

		// Format-aware routing: OpenAI-native providers (e.g. Copilot) skip translation.
		if prov.APIFormat() == "openai" {
			h.serveOpenAINative(w, r, &oaiReq, body, &mapping, prov, proxyKeyID, start, attemptCount)
			return
		}

		// Format-aware routing: Responses API providers require CC→Responses translation.
		if prov.APIFormat() == "responses" {
			h.serveOpenAIViaResponsesProvider(w, r, &oaiReq, body, &mapping, prov, proxyKeyID, start, attemptCount)
			return
		}

		// Translate OpenAI request to Anthropic format.
		oaiReqCopy := oaiReq
		oaiReqCopy.Model = mapping.UpstreamModel
		anthReq, err := translate.OpenAIToAnthropic(&oaiReqCopy)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
			return
		}

		// Serialize the Anthropic request for the provider.
		anthBody, err := json.Marshal(anthReq)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
			return
		}

		// Build the provider request.
		provReq := &provider.Request{
			Body:        anthBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: oaiReq.Stream,
		}

		// Send the request upstream.
		provResp, err := prov.SendRequest(r.Context(), provReq)
		if err != nil {
			// Network error — try next entry if available.
			if attempt < len(chain)-1 {
				continue
			}
			latency := time.Since(start).Milliseconds()
			errMsg := err.Error()
			h.logger.logEntry(proxyKeyID, "openai", provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
				latency, body, attemptCount, handlerResult{
					Status: http.StatusBadGateway, Body: []byte(errMsg),
					ErrDetails: &errMsg, IsStreaming: oaiReq.Stream,
				})
			writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
			return
		}

		// Check if we should fall back due to the response status.
		if isFallbackStatus(provResp.StatusCode) {
			if provResp.Stream != nil {
				_ = provResp.Stream.Close()
			}
			if attempt < len(chain)-1 {
				continue
			}
			// All entries exhausted.
			latency := time.Since(start).Milliseconds()
			errMsg := fmt.Sprintf("all %d fallback entries exhausted; last status %d", attemptCount, provResp.StatusCode)
			h.logger.logEntry(proxyKeyID, "openai", provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
				latency, body, attemptCount, handlerResult{
					Status: http.StatusServiceUnavailable, Body: []byte(errMsg),
					ErrDetails: &errMsg, IsStreaming: oaiReq.Stream,
				})
			writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "All upstream providers failed")
			return
		}

		// Success.
		provKey := provKeyFor(h.cfg, prov)
		var result handlerResult
		if oaiReq.Stream {
			result = h.handleOpenAIStreaming(w, provResp, mapping.ModelName)
		} else {
			result = h.handleOpenAINonStreaming(w, provResp, mapping.ModelName)
		}
		latency := time.Since(start).Milliseconds()
		h.logger.logEntry(proxyKeyID, "openai", provKey, mapping.ModelName, mapping.UpstreamModel,
			latency, body, attemptCount, result)
		return
	}
}

func (h *OpenAIHandler) handleOpenAINonStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		// Translate Anthropic error to OpenAI format.
		oaiErr, err := translate.AnthropicErrorToOpenAI(resp.Body)
		if err != nil {
			oaiErr = resp.Body
		}
		s := string(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(oaiErr); err != nil {
			slog.Warn("write OpenAI error response", "error", err)
		}
		return handlerResult{
			InputTokens:  resp.InputTokens,
			OutputTokens: resp.OutputTokens,
			Status:       resp.StatusCode,
			Body:         resp.Body,
			ErrDetails:   &s,
		}
	}

	// Parse the Anthropic response.
	var anthResp anthropic.MessagesResponse
	if err := json.Unmarshal(resp.Body, &anthResp); err != nil {
		s := fmt.Sprintf("failed to parse upstream response: %v", err)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return handlerResult{
			Status:     http.StatusBadGateway,
			Body:       resp.Body,
			ErrDetails: &s,
		}
	}

	// Translate to OpenAI format.
	oaiResp := translate.AnthropicToOpenAI(&anthResp, modelRequested)
	oaiBody, err := json.Marshal(oaiResp)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(oaiBody); err != nil {
		slog.Warn("write OpenAI response", "error", err)
	}
	return handlerResult{
		InputTokens:              anthResp.Usage.InputTokens,
		OutputTokens:             anthResp.Usage.OutputTokens,
		CacheCreationInputTokens: anthResp.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     anthResp.Usage.CacheReadInputTokens,
		Status:                   http.StatusOK,
		Body:                     oaiBody,
	}
}

func (h *OpenAIHandler) handleOpenAIStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.SSEStream(w, resp.Stream, modelRequested)

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

// serveOpenAINative handles requests to OpenAI-native providers (e.g. Copilot)
// without translating through Anthropic format.
func (h *OpenAIHandler) serveOpenAINative(w http.ResponseWriter, r *http.Request,
	oaiReq *translate.ChatCompletionRequest, rawBody []byte,
	mapping *config.ModelMapping, prov provider.Provider, proxyKeyID string, start time.Time, attemptCount int64,
) {
	// Replace model name in the raw JSON body to preserve all original fields.
	var bodyMap map[string]any
	if err := json.Unmarshal(rawBody, &bodyMap); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}
	bodyMap["model"] = mapping.UpstreamModel
	nativeBody, err := json.Marshal(bodyMap)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	provReq := &provider.Request{
		Body:        nativeBody,
		Headers:     r.Header.Clone(),
		Model:       mapping.UpstreamModel,
		IsStreaming: oaiReq.Stream,
	}

	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		errMsg := err.Error()
		h.logger.logEntry(proxyKeyID, "openai", provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
			latency, rawBody, attemptCount, handlerResult{
				Status: http.StatusBadGateway, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: oaiReq.Stream,
			})
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	provKey := provKeyFor(h.cfg, prov)
	var result handlerResult
	if oaiReq.Stream {
		result = h.handleOpenAINativeStreaming(w, provResp)
	} else {
		result = h.handleOpenAINativeNonStreaming(w, provResp)
	}
	latency := time.Since(start).Milliseconds()
	h.logger.logEntry(proxyKeyID, "openai", provKey, mapping.ModelName, mapping.UpstreamModel,
		latency, rawBody, attemptCount, result)
}

func (h *OpenAIHandler) handleOpenAINativeNonStreaming(w http.ResponseWriter, resp *provider.Response) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(resp.Body); err != nil {
			slog.Warn("write OpenAI native error response", "error", err)
		}
		return handlerResult{
			InputTokens:     resp.InputTokens,
			OutputTokens:    resp.OutputTokens,
			ReasoningTokens: resp.ReasoningTokens,
			Status:          resp.StatusCode,
			Body:            resp.Body,
			ErrDetails:      &s,
		}
	}

	// Forward response as-is — token usage already extracted by the provider client.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(resp.Body); err != nil {
		slog.Warn("write OpenAI native response", "error", err)
	}
	return handlerResult{
		InputTokens:              resp.InputTokens,
		OutputTokens:             resp.OutputTokens,
		CacheCreationInputTokens: resp.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.CacheReadInputTokens,
		ReasoningTokens:          resp.ReasoningTokens,
		Status:                   http.StatusOK,
		Body:                     resp.Body,
	}
}

func (h *OpenAIHandler) handleOpenAINativeStreaming(w http.ResponseWriter, resp *provider.Response) handlerResult {
	result, err := RelayOpenAISSEStream(w, resp.Stream)

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

// serveOpenAIViaResponsesProvider handles CC requests that need to be sent
// to a Responses API upstream. Translates CC→Responses on request and
// Responses→CC on response.
func (h *OpenAIHandler) serveOpenAIViaResponsesProvider(w http.ResponseWriter, r *http.Request,
	oaiReq *translate.ChatCompletionRequest, rawBody []byte,
	mapping *config.ModelMapping, prov provider.Provider, proxyKeyID string, start time.Time, attemptCount int64,
) {
	// Translate CC to Responses API format.
	oaiReqCopy := *oaiReq
	oaiReqCopy.Model = mapping.UpstreamModel
	respReq, err := translate.OpenAIToResponses(&oaiReqCopy, mapping.UpstreamModel)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return
	}

	respBody, err := json.Marshal(respReq)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	provReq := &provider.Request{
		Body:        respBody,
		Headers:     r.Header.Clone(),
		Model:       mapping.UpstreamModel,
		IsStreaming: oaiReq.Stream,
	}

	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		errMsg := err.Error()
		h.logger.logEntry(proxyKeyID, "openai", provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
			latency, rawBody, attemptCount, handlerResult{
				Status: http.StatusBadGateway, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: oaiReq.Stream,
			})
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	provKey := provKeyFor(h.cfg, prov)
	var result handlerResult
	if oaiReq.Stream {
		result = h.handleResponsesProviderStreamingToCC(w, provResp, mapping.ModelName)
	} else {
		result = h.handleResponsesProviderNonStreamingToCC(w, provResp, mapping.ModelName)
	}
	latency := time.Since(start).Milliseconds()
	h.logger.logEntry(proxyKeyID, "openai", provKey, mapping.ModelName, mapping.UpstreamModel,
		latency, rawBody, attemptCount, result)
}

func (h *OpenAIHandler) handleResponsesProviderNonStreamingToCC(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeOpenAIError(w, resp.StatusCode, "api_error", s)
		return handlerResult{
			InputTokens:     resp.InputTokens,
			OutputTokens:    resp.OutputTokens,
			ReasoningTokens: resp.ReasoningTokens,
			Status:          resp.StatusCode,
			Body:            resp.Body,
			ErrDetails:      &s,
		}
	}

	// Translate Responses API response to CC format.
	ccResp := translate.ResponsesToOpenAIResponse(resp.Body, modelRequested)
	ccBody, err := json.Marshal(ccResp)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(ccBody); err != nil {
		slog.Warn("write OpenAI response", "error", err)
	}
	return handlerResult{
		InputTokens:     resp.InputTokens,
		OutputTokens:    resp.OutputTokens,
		ReasoningTokens: resp.ReasoningTokens,
		Status:          http.StatusOK,
		Body:            ccBody,
	}
}

func (h *OpenAIHandler) handleResponsesProviderStreamingToCC(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.ResponsesSSEToOpenAISSE(w, resp.Stream, modelRequested)

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

// writeOpenAIError writes an error response in OpenAI format.
func writeOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(translate.OpenAIErrorResponse{
		Error: translate.OpenAIError{
			Message: message,
			Type:    errType,
		},
	}); err != nil {
		slog.Warn("write OpenAI error", "error", err)
	}
}

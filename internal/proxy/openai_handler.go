package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
	anthropic "codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
	"codeberg.org/kglitchy/glitchgate/internal/store"
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
			h.logOpenAIRequest(proxyKeyID, provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
				0, 0, 0, 0, latency, http.StatusBadGateway, body, []byte(errMsg), nil, &errMsg, oaiReq.Stream, attemptCount)
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
			h.logOpenAIRequest(proxyKeyID, provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
				0, 0, 0, 0, latency, http.StatusServiceUnavailable, body, []byte(errMsg), nil, &errMsg, oaiReq.Stream, attemptCount)
			writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "All upstream providers failed")
			return
		}

		// Success.
		provKey := provKeyFor(h.cfg, prov)
		if oaiReq.Stream {
			h.handleOpenAIStreaming(w, provResp, proxyKeyID, provKey, mapping.ModelName, mapping.UpstreamModel, body, start, attemptCount)
		} else {
			h.handleOpenAINonStreaming(w, provResp, proxyKeyID, provKey, mapping.ModelName, mapping.UpstreamModel, body, start, attemptCount)
		}
		return
	}
}

func (h *OpenAIHandler) handleOpenAINonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if resp.StatusCode >= 400 {
		// Translate Anthropic error to OpenAI format.
		oaiErr, err := translate.AnthropicErrorToOpenAI(resp.Body)
		if err != nil {
			oaiErr = resp.Body
		}
		s := string(resp.Body)
		errDetails = &s
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(oaiErr); err != nil {
			log.Printf("WARNING: write OpenAI error response: %v", err)
		}

		h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
			resp.InputTokens, resp.OutputTokens, 0, 0, latency, resp.StatusCode,
			reqBody, resp.Body, nil, errDetails, false, attemptCount)
		return
	}

	// Parse the Anthropic response.
	var anthResp anthropic.MessagesResponse
	if err := json.Unmarshal(resp.Body, &anthResp); err != nil {
		s := fmt.Sprintf("failed to parse upstream response: %v", err)
		errDetails = &s
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
			0, 0, 0, 0, latency, http.StatusBadGateway,
			reqBody, resp.Body, nil, errDetails, false, attemptCount)
		return
	}

	// Translate to OpenAI format.
	oaiResp := translate.AnthropicToOpenAI(&anthResp, modelRequested)
	oaiBody, err := json.Marshal(oaiResp)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(oaiBody); err != nil {
		log.Printf("WARNING: write OpenAI response: %v", err)
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens,
		anthResp.Usage.CacheCreationInputTokens, anthResp.Usage.CacheReadInputTokens)

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens,
		anthResp.Usage.CacheCreationInputTokens, anthResp.Usage.CacheReadInputTokens,
		latency, http.StatusOK,
		reqBody, oaiBody, cost, nil, false, attemptCount)
}

func (h *OpenAIHandler) handleOpenAIStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	result, err := translate.SSEStream(w, resp.Stream, modelRequested)
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream relay error: %v", err)
		errDetails = &s
		log.Printf("WARNING: %s", s)
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, result.InputTokens, result.OutputTokens,
		result.CacheCreationInputTokens, result.CacheReadInputTokens)

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens,
		result.CacheCreationInputTokens, result.CacheReadInputTokens,
		latency, status,
		reqBody, result.Body, cost, errDetails, true, attemptCount)
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
		h.logOpenAIRequest(proxyKeyID, provKeyFor(h.cfg, prov), mapping.ModelName, mapping.UpstreamModel,
			0, 0, 0, 0, latency, http.StatusBadGateway, rawBody, []byte(errMsg), nil, &errMsg, oaiReq.Stream, attemptCount)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	provKey := provKeyFor(h.cfg, prov)
	if oaiReq.Stream {
		h.handleOpenAINativeStreaming(w, provResp, proxyKeyID, provKey, mapping.ModelName, mapping.UpstreamModel, rawBody, start, attemptCount)
	} else {
		h.handleOpenAINativeNonStreaming(w, provResp, proxyKeyID, provKey, mapping.ModelName, mapping.UpstreamModel, rawBody, start, attemptCount)
	}
}

func (h *OpenAIHandler) handleOpenAINativeNonStreaming(w http.ResponseWriter, resp *provider.Response,
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
			log.Printf("WARNING: write OpenAI native error response: %v", err)
		}

		h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
			resp.InputTokens, resp.OutputTokens, 0, 0, latency, resp.StatusCode,
			reqBody, resp.Body, nil, errDetails, false, attemptCount)
		return
	}

	// Forward response as-is — token usage already extracted by the provider client.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(resp.Body); err != nil {
		log.Printf("WARNING: write OpenAI native response: %v", err)
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, resp.InputTokens, resp.OutputTokens, 0, 0)

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		resp.InputTokens, resp.OutputTokens, 0, 0,
		latency, http.StatusOK,
		reqBody, resp.Body, cost, nil, false, attemptCount)
}

func (h *OpenAIHandler) handleOpenAINativeStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	result, err := RelayOpenAISSEStream(w, resp.Stream)
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream relay error: %v", err)
		errDetails = &s
		log.Printf("WARNING: %s", s)
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, result.InputTokens, result.OutputTokens, 0, 0)

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, 0, 0,
		latency, status,
		reqBody, result.Body, cost, errDetails, true, attemptCount)
}

func (h *OpenAIHandler) logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream string,
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, latencyMs int64, status int,
	requestBody, responseBody []byte, cost *float64, errDetails *string, isStreaming bool, attemptCount int64,
) {
	entry := &store.RequestLogEntry{
		ID:                       uuid.New().String(),
		ProxyKeyID:               proxyKeyID,
		Timestamp:                time.Now().UTC(),
		SourceFormat:             "openai",
		ProviderName:             providerName,
		ModelRequested:           modelRequested,
		ModelUpstream:            modelUpstream,
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		LatencyMs:                latencyMs,
		Status:                   status,
		RequestBody:              RedactRequestBody(requestBody),
		ResponseBody:             string(responseBody),
		EstimatedCostUSD:         cost,
		ErrorDetails:             errDetails,
		IsStreaming:              isStreaming,
		FallbackAttempts:         attemptCount,
	}

	h.logger.Log(entry)
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
		log.Printf("WARNING: write OpenAI error: %v", err)
	}
}

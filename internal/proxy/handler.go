// Package proxy implements the core HTTP proxy handlers for Anthropic and OpenAI-compatible requests.
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
	"codeberg.org/kglitchy/glitchgate/internal/provider/copilot"
	"codeberg.org/kglitchy/glitchgate/internal/store"
	"codeberg.org/kglitchy/glitchgate/internal/translate"
)

// provKeyFor returns the pricing.ProviderKey for the given provider, looking up
// its type and base URL from config. Falls back to the provider name on error.
func provKeyFor(cfg *config.Config, prov provider.Provider) string {
	pc, err := cfg.FindProvider(prov.Name())
	if err != nil {
		return prov.Name()
	}
	baseURL := pc.BaseURL
	if pc.Type == "github_copilot" && baseURL == "" {
		baseURL = copilot.DefaultAPIURL
	}
	return pricing.ProviderKey(pc.Type, baseURL)
}

// Handler is the core proxy HTTP handler for Anthropic-compatible requests.
type Handler struct {
	cfg        *config.Config
	providers  map[string]provider.Provider
	calculator *pricing.Calculator
	logger     *AsyncLogger
}

// NewHandler creates a new proxy handler.
func NewHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger) *Handler {
	return &Handler{
		cfg:        cfg,
		providers:  providers,
		calculator: calc,
		logger:     logger,
	}
}

// isFallbackStatus reports whether an HTTP status code should trigger a fallback
// attempt. Retries happen on 5xx (server errors) and 429 (rate limited).
func isFallbackStatus(code int) bool {
	return code >= 500 || code == 429
}

// ServeHTTP handles POST /v1/messages requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	start := time.Now()

	// Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	// Parse just enough to extract model and stream flag.
	var reqBody struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &reqBody); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}

	if reqBody.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Missing required field: model")
		return
	}

	// Resolve model chain.
	chain, err := h.cfg.FindModel(reqBody.Model)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model: %s", reqBody.Model))
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
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", mapping.Provider))
			return
		}

		// Format-aware routing: OpenAI-native providers require Anthropic→OpenAI translation.
		if prov.APIFormat() == "openai" {
			h.serveViaOpenAIProvider(w, r, body, &reqBody, &mapping, prov, proxyKeyID, start, attemptCount)
			return
		}

		// Replace the model name in the request body with the upstream model name.
		var bodyMap map[string]any
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
			return
		}
		bodyMap["model"] = mapping.UpstreamModel
		upstreamBody, err := json.Marshal(bodyMap)
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
			return
		}

		// Build the provider request.
		provReq := &provider.Request{
			Body:        upstreamBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: reqBody.Stream,
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
			provKey := provKeyFor(h.cfg, prov)
			h.logRequest(proxyKeyID, "anthropic", provKey, reqBody.Model, mapping.UpstreamModel,
				0, 0, 0, 0, latency, http.StatusBadGateway, upstreamBody, []byte(errMsg), nil, &errMsg, reqBody.Stream, attemptCount)
			writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
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
			// All entries exhausted — return the last error response.
			latency := time.Since(start).Milliseconds()
			errMsg := fmt.Sprintf("all %d fallback entries exhausted; last status %d", attemptCount, provResp.StatusCode)
			provKey := provKeyFor(h.cfg, prov)
			h.logRequest(proxyKeyID, "anthropic", provKey, reqBody.Model, mapping.UpstreamModel,
				0, 0, 0, 0, latency, http.StatusServiceUnavailable, upstreamBody, []byte(errMsg), nil, &errMsg, reqBody.Stream, attemptCount)
			writeAnthropicError(w, http.StatusServiceUnavailable, "api_error", "All upstream providers failed")
			return
		}

		// Success — dispatch to streaming or non-streaming handler.
		provKey := provKeyFor(h.cfg, prov)
		if reqBody.Stream {
			h.handleStreaming(w, r, provResp, proxyKeyID, provKey, reqBody.Model, mapping.UpstreamModel, upstreamBody, start, attemptCount)
		} else {
			h.handleNonStreaming(w, provResp, proxyKeyID, provKey, reqBody.Model, mapping.UpstreamModel, upstreamBody, start, attemptCount)
		}
		return
	}
}

func (h *Handler) handleNonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	latency := time.Since(start).Milliseconds()

	// Forward response headers.
	for k, vals := range resp.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(resp.Body); err != nil {
		log.Printf("WARNING: write response body: %v", err)
	}

	var errDetails *string
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		errDetails = &s
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, resp.InputTokens, resp.OutputTokens,
		resp.CacheCreationInputTokens, resp.CacheReadInputTokens)

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		resp.InputTokens, resp.OutputTokens, resp.CacheCreationInputTokens, resp.CacheReadInputTokens,
		latency, resp.StatusCode, reqBody, resp.Body, cost, errDetails, false, attemptCount)
}

func (h *Handler) handleStreaming(w http.ResponseWriter, _ *http.Request, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	result, err := RelaySSEStream(w, resp.Stream)
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

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, result.CacheCreationInputTokens, result.CacheReadInputTokens,
		latency, status, reqBody, result.Body, cost, errDetails, true, attemptCount)
}

// serveViaOpenAIProvider handles Anthropic-format requests that need to be sent
// to an OpenAI-native provider. It translates Anthropic→OpenAI on request and
// OpenAI→Anthropic on response.
func (h *Handler) serveViaOpenAIProvider(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}, mapping *config.ModelMapping, prov provider.Provider, proxyKeyID string, start time.Time, attemptCount int64,
) {
	// Parse the full Anthropic request for translation.
	var anthReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthReq); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}

	// Translate to OpenAI format.
	anthReq.Model = mapping.UpstreamModel
	oaiReq, err := translate.AnthropicToOpenAIRequest(&anthReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return
	}

	oaiBody, err := json.Marshal(oaiReq)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	provReq := &provider.Request{
		Body:        oaiBody,
		Headers:     r.Header.Clone(),
		Model:       mapping.UpstreamModel,
		IsStreaming: reqBody.Stream,
	}

	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		errMsg := err.Error()
		provKey := provKeyFor(h.cfg, prov)
		h.logRequest(proxyKeyID, "anthropic", provKey, reqBody.Model, mapping.UpstreamModel,
			0, 0, 0, 0, latency, http.StatusBadGateway, body, []byte(errMsg), nil, &errMsg, reqBody.Stream, attemptCount)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	provKey := provKeyFor(h.cfg, prov)
	if reqBody.Stream {
		h.handleOpenAIProviderStreaming(w, provResp, proxyKeyID, provKey, reqBody.Model, mapping.UpstreamModel, body, start, attemptCount)
	} else {
		h.handleOpenAIProviderNonStreaming(w, provResp, proxyKeyID, provKey, reqBody.Model, mapping.UpstreamModel, body, start, attemptCount)
	}
}

func (h *Handler) handleOpenAIProviderNonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if resp.StatusCode >= 400 {
		// Translate OpenAI error to Anthropic format.
		s := string(resp.Body)
		errDetails = &s
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
		h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
			resp.InputTokens, resp.OutputTokens, 0, 0, latency, resp.StatusCode,
			reqBody, resp.Body, nil, errDetails, false, attemptCount)
		return
	}

	// Parse the OpenAI response.
	var oaiResp translate.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &oaiResp); err != nil {
		s := fmt.Sprintf("failed to parse upstream response: %v", err)
		errDetails = &s
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
			0, 0, 0, 0, latency, http.StatusBadGateway,
			reqBody, resp.Body, nil, errDetails, false, attemptCount)
		return
	}

	// Translate to Anthropic format.
	anthResp := translate.OpenAIToAnthropicResponse(&oaiResp, modelRequested)
	anthBody, err := json.Marshal(anthResp)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(anthBody); err != nil {
		log.Printf("WARNING: write Anthropic response: %v", err)
	}

	cost := h.calculator.Calculate(providerName, modelUpstream, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, 0, 0)

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, 0, 0,
		latency, http.StatusOK,
		reqBody, anthBody, cost, nil, false, attemptCount)
}

func (h *Handler) handleOpenAIProviderStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time, attemptCount int64,
) {
	result, err := translate.ReverseSSEStream(w, resp.Stream, modelRequested)
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

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, 0, 0,
		latency, status, reqBody, result.Body, cost, errDetails, true, attemptCount)
}

func (h *Handler) logRequest(proxyKeyID, sourceFormat, providerName, modelRequested, modelUpstream string,
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, latencyMs int64, status int,
	requestBody, responseBody []byte, cost *float64, errDetails *string, isStreaming bool, attemptCount int64,
) {
	entry := &store.RequestLogEntry{
		ID:                       uuid.New().String(),
		ProxyKeyID:               proxyKeyID,
		Timestamp:                time.Now().UTC(),
		SourceFormat:             sourceFormat,
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

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

	"codeberg.org/kglitchy/llm-proxy/internal/config"
	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/provider"
	anthropic "codeberg.org/kglitchy/llm-proxy/internal/provider/anthropic"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
	"codeberg.org/kglitchy/llm-proxy/internal/translate"
)

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

	// Resolve model mapping.
	modelMapping, err := h.cfg.FindModel(reqBody.Model)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model: %s", reqBody.Model))
		return
	}

	// Find the provider.
	prov, ok := h.providers[modelMapping.Provider]
	if !ok {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", modelMapping.Provider))
		return
	}

	// Get the authenticated proxy key for logging.
	pk := KeyFromContext(r.Context())
	proxyKeyID := ""
	if pk != nil {
		proxyKeyID = pk.ID
	}

	// Format-aware routing: OpenAI-native providers require Anthropic→OpenAI translation.
	if prov.APIFormat() == "openai" {
		h.serveViaOpenAIProvider(w, r, body, &reqBody, modelMapping, prov, proxyKeyID, start)
		return
	}

	// Replace the model name in the request body with the upstream model name.
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return
	}
	bodyMap["model"] = modelMapping.UpstreamModel
	body, err = json.Marshal(bodyMap)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return
	}

	// Build the provider request.
	provReq := &provider.Request{
		Body:        body,
		Headers:     r.Header.Clone(),
		Model:       modelMapping.UpstreamModel,
		IsStreaming: reqBody.Stream,
	}

	// Send the request upstream.
	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		errMsg := err.Error()
		h.logRequest(proxyKeyID, "anthropic", prov.Name(), reqBody.Model, modelMapping.UpstreamModel,
			0, 0, 0, 0, latency, http.StatusBadGateway, body, []byte(errMsg), nil, &errMsg, reqBody.Stream)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	if reqBody.Stream {
		h.handleStreaming(w, r, provResp, proxyKeyID, prov.Name(), reqBody.Model, modelMapping.UpstreamModel, body, start)
	} else {
		h.handleNonStreaming(w, provResp, proxyKeyID, prov.Name(), reqBody.Model, modelMapping.UpstreamModel, body, start)
	}
}

func (h *Handler) handleNonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
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

	cost := h.calculator.Calculate(modelUpstream, resp.InputTokens, resp.OutputTokens,
		resp.CacheCreationInputTokens, resp.CacheReadInputTokens)

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		resp.InputTokens, resp.OutputTokens, resp.CacheCreationInputTokens, resp.CacheReadInputTokens,
		latency, resp.StatusCode, reqBody, resp.Body, cost, errDetails, false)
}

func (h *Handler) handleStreaming(w http.ResponseWriter, _ *http.Request, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
) {
	result, err := RelaySSEStream(w, resp.Stream)
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream relay error: %v", err)
		errDetails = &s
		log.Printf("WARNING: %s", s)
	}

	cost := h.calculator.Calculate(modelUpstream, result.InputTokens, result.OutputTokens,
		result.CacheCreationInputTokens, result.CacheReadInputTokens)

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, result.CacheCreationInputTokens, result.CacheReadInputTokens,
		latency, status, reqBody, result.Body, cost, errDetails, true)
}

// serveViaOpenAIProvider handles Anthropic-format requests that need to be sent
// to an OpenAI-native provider. It translates Anthropic→OpenAI on request and
// OpenAI→Anthropic on response.
func (h *Handler) serveViaOpenAIProvider(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}, mapping *config.ModelMapping, prov provider.Provider, proxyKeyID string, start time.Time,
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
		h.logRequest(proxyKeyID, "anthropic", prov.Name(), reqBody.Model, mapping.UpstreamModel,
			0, 0, 0, 0, latency, http.StatusBadGateway, body, []byte(errMsg), nil, &errMsg, reqBody.Stream)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	if reqBody.Stream {
		h.handleOpenAIProviderStreaming(w, provResp, proxyKeyID, prov.Name(), reqBody.Model, mapping.UpstreamModel, body, start)
	} else {
		h.handleOpenAIProviderNonStreaming(w, provResp, proxyKeyID, prov.Name(), reqBody.Model, mapping.UpstreamModel, body, start)
	}
}

func (h *Handler) handleOpenAIProviderNonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
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
			reqBody, resp.Body, nil, errDetails, false)
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
			reqBody, resp.Body, nil, errDetails, false)
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

	cost := h.calculator.Calculate(modelUpstream, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, 0, 0)

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens, 0, 0,
		latency, http.StatusOK,
		reqBody, anthBody, cost, nil, false)
}

func (h *Handler) handleOpenAIProviderStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
) {
	result, err := translate.ReverseSSEStream(w, resp.Stream, modelRequested)
	latency := time.Since(start).Milliseconds()

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream relay error: %v", err)
		errDetails = &s
		log.Printf("WARNING: %s", s)
	}

	cost := h.calculator.Calculate(modelUpstream, result.InputTokens, result.OutputTokens, 0, 0)

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	h.logRequest(proxyKeyID, "anthropic", providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens, 0, 0,
		latency, status, reqBody, result.Body, cost, errDetails, true)
}

func (h *Handler) logRequest(proxyKeyID, sourceFormat, providerName, modelRequested, modelUpstream string,
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, latencyMs int64, status int,
	requestBody, responseBody []byte, cost *float64, errDetails *string, isStreaming bool,
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
	}

	h.logger.Log(entry)
}

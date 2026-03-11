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

	// Resolve model mapping.
	modelMapping, err := h.cfg.FindModel(oaiReq.Model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Unknown model: %s", oaiReq.Model))
		return
	}

	// Find the provider.
	prov, ok := h.providers[modelMapping.Provider]
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", modelMapping.Provider))
		return
	}

	// Translate OpenAI request to Anthropic format.
	oaiReq.Model = modelMapping.UpstreamModel
	anthReq, err := translate.OpenAIToAnthropic(&oaiReq)
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
		Model:       modelMapping.UpstreamModel,
		IsStreaming: oaiReq.Stream,
	}

	// Get the authenticated proxy key for logging.
	pk := KeyFromContext(r.Context())
	proxyKeyID := ""
	if pk != nil {
		proxyKeyID = pk.ID
	}

	// Send the request upstream.
	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		errMsg := err.Error()
		h.logOpenAIRequest(proxyKeyID, prov.Name(), modelMapping.ModelName, modelMapping.UpstreamModel,
			0, 0, 0, 0, latency, http.StatusBadGateway, body, []byte(errMsg), nil, &errMsg, oaiReq.Stream)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to reach upstream provider")
		return
	}

	if oaiReq.Stream {
		h.handleOpenAIStreaming(w, provResp, proxyKeyID, prov.Name(), modelMapping.ModelName, modelMapping.UpstreamModel, body, start)
	} else {
		h.handleOpenAINonStreaming(w, provResp, proxyKeyID, prov.Name(), modelMapping.ModelName, modelMapping.UpstreamModel, body, start)
	}
}

func (h *OpenAIHandler) handleOpenAINonStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
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
			reqBody, resp.Body, nil, errDetails, false)
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
			reqBody, resp.Body, nil, errDetails, false)
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

	cost := h.calculator.Calculate(modelUpstream, anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens,
		anthResp.Usage.CacheCreationInputTokens, anthResp.Usage.CacheReadInputTokens)

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		anthResp.Usage.InputTokens, anthResp.Usage.OutputTokens,
		anthResp.Usage.CacheCreationInputTokens, anthResp.Usage.CacheReadInputTokens,
		latency, http.StatusOK,
		reqBody, oaiBody, cost, nil, false)
}

func (h *OpenAIHandler) handleOpenAIStreaming(w http.ResponseWriter, resp *provider.Response,
	proxyKeyID, providerName, modelRequested, modelUpstream string, reqBody []byte, start time.Time,
) {
	result, err := translate.SSEStream(w, resp.Stream, modelRequested)
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

	h.logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream,
		result.InputTokens, result.OutputTokens,
		result.CacheCreationInputTokens, result.CacheReadInputTokens,
		latency, status,
		reqBody, result.Body, cost, errDetails, true)
}

func (h *OpenAIHandler) logOpenAIRequest(proxyKeyID, providerName, modelRequested, modelUpstream string,
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, latencyMs int64, status int,
	requestBody, responseBody []byte, cost *float64, errDetails *string, isStreaming bool,
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

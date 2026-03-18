// Package proxy implements the core HTTP proxy handlers for Anthropic and OpenAI-compatible requests.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/metrics"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	anthropic "github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/translate"
)

// Handler is the core proxy HTTP handler for Anthropic-compatible requests.
type Handler struct {
	cfg           *config.Config
	providers     map[string]provider.Provider
	calculator    *pricing.Calculator
	logger        *AsyncLogger
	budgetChecker *BudgetChecker
}

// NewHandler creates a new proxy handler.
func NewHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger, bc *BudgetChecker) *Handler {
	return &Handler{
		cfg:           cfg,
		providers:     providers,
		calculator:    calc,
		logger:        logger,
		budgetChecker: bc,
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

	body, ok := readRequestBodyWithLimit(w, r, h.cfg.ProxyMaxBodyBytes, "invalid_request_error", writeAnthropicError)
	if !ok {
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
	keyPrefix := ""
	if pk != nil {
		proxyKeyID = pk.ID
		keyPrefix = pk.KeyPrefix
	}

	metrics.RecordActiveRequest("anthropic")
	defer metrics.FinishActiveRequest("anthropic")

	if h.budgetChecker != nil && proxyKeyID != "" {
		if violation, err := h.budgetChecker.Check(r.Context(), proxyKeyID); err != nil {
			slog.Warn("budget check error", "error", err)
		} else if violation != nil {
			writeAnthropicError(w, http.StatusTooManyRequests, "budget_exceeded",
				fmt.Sprintf("Budget exceeded: %s %s limit $%.2f, spent $%.2f, resets %s",
					violation.Scope, violation.Period, violation.LimitUSD, violation.SpendUSD,
					violation.ResetAt.In(h.budgetChecker.tz).Format(time.RFC3339)))
			return
		}
	}

	executeProxyPipeline(w, r, h.logger, chain, h.providers, pipelineSpec{
		SourceFormat: "anthropic",
		ProxyKeyID:   proxyKeyID,
		KeyPrefix:    keyPrefix,
		ModelRequest: reqBody.Model,
		IsStreaming:  reqBody.Stream,
		Start:        start,
		Calculator:   h.calculator,
	}, h.routeBuilders(w, r, body, &reqBody),
		newPipelineCallbacks(w, writeAnthropicError, "api_error"))
}

func (h *Handler) handleNonStreaming(w http.ResponseWriter, resp *provider.Response) handlerResult {
	var errDetails *string
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		errDetails = &s
	}
	// Forward response headers.
	for k, vals := range resp.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(resp.Body); err != nil {
		slog.Warn("write response body", "error", err)
	}
	return handlerResult{
		InputTokens:              resp.InputTokens,
		OutputTokens:             resp.OutputTokens,
		CacheCreationInputTokens: resp.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.CacheReadInputTokens,
		Status:                   resp.StatusCode,
		Body:                     resp.Body,
		ErrDetails:               errDetails,
	}
}

func (h *Handler) handleStreaming(ctx context.Context, w http.ResponseWriter, resp *provider.Response) handlerResult {
	// Forward upstream response headers (e.g. anthropic-beta) before the SSE
	// relay sets Content-Type and calls WriteHeader. Claude Code validates these
	// headers before rendering thinking blocks.
	for k, vals := range resp.Headers {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "anthropic-") || lower == "x-request-id" {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
	}
	result, err := RelaySSEStream(ctx, w, resp.Stream)
	return streamingResult(resp, result, err)
}

func (h *Handler) routeBuilders(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	},
) map[string]routeBuilder {
	return map[string]routeBuilder{
		"anthropic": func(attempt chainAttempt) (*routePlan, bool) {
			mapping := attempt.Mapping
			var bodyMap map[string]any
			if err := json.Unmarshal(body, &bodyMap); err != nil {
				writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
				return nil, true
			}
			bodyMap["model"] = mapping.UpstreamModel
			upstreamBody, err := json.Marshal(bodyMap)
			if err != nil {
				writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
				return nil, true
			}
			return &routePlan{
				ProviderRequest: &provider.Request{
					Body:        upstreamBody,
					Headers:     r.Header.Clone(),
					Model:       mapping.UpstreamModel,
					IsStreaming: reqBody.Stream,
				},
				RequestBody: upstreamBody,
				HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
					if provResp.StatusCode >= 400 {
						return h.handleNonStreaming(w, provResp)
					}
					if reqBody.Stream {
						return h.handleStreaming(r.Context(), w, provResp)
					}
					return h.handleNonStreaming(w, provResp)
				},
			}, false
		},
		"openai": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildOpenAIProviderRoute(w, r, body, reqBody, &attempt.Mapping)
		},
		"responses": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildResponsesProviderRoute(w, r, body, reqBody, &attempt.Mapping)
		},
		"gemini": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildGeminiProviderRoute(w, r, body, reqBody, &attempt.Mapping)
		},
	}
}

// buildOpenAIProviderRoute handles Anthropic-format requests that need to be
// sent to an OpenAI-native provider. It translates Anthropic→OpenAI on request
// and OpenAI→Anthropic on response.
func (h *Handler) buildOpenAIProviderRoute(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}, mapping *config.DispatchTarget,
) (*routePlan, bool) {
	// Parse the full Anthropic request for translation.
	var anthReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthReq); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return nil, true
	}

	// Translate to OpenAI format.
	anthReq.Model = mapping.UpstreamModel
	oaiReq, err := translate.AnthropicToOpenAIRequest(&anthReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	// Check if this provider forces non-streaming upstream calls.
	forceNonStream := providerForcesNonStream(h.cfg, mapping.Provider)
	if forceNonStream {
		oaiReq.Stream = false
	}

	oaiBody, err := json.Marshal(oaiReq)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        oaiBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: reqBody.Stream && !forceNonStream,
		},
		RequestBody: body,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleOpenAIProviderNonStreaming(w, provResp, reqBody.Model)
			}
			switch {
			case reqBody.Stream && forceNonStream:
				return h.handleOpenAIProviderForcedStream(w, provResp, reqBody.Model)
			case reqBody.Stream:
				return h.handleOpenAIProviderStreaming(w, r, provResp, reqBody.Model)
			default:
				return h.handleOpenAIProviderNonStreaming(w, provResp, reqBody.Model)
			}
		},
	}, false
}

func (h *Handler) handleOpenAIProviderNonStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
		return handlerResult{
			InputTokens:     resp.InputTokens,
			OutputTokens:    resp.OutputTokens,
			ReasoningTokens: resp.ReasoningTokens,
			Status:          resp.StatusCode,
			Body:            resp.Body,
			ErrDetails:      &s,
		}
	}

	// Parse the OpenAI response.
	var oaiResp openai.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &oaiResp); err != nil {
		s := fmt.Sprintf("failed to parse upstream response: %v", err)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return handlerResult{
			Status:     http.StatusBadGateway,
			Body:       resp.Body,
			ErrDetails: &s,
		}
	}

	// Translate to Anthropic format.
	anthResp := translate.OpenAIToAnthropicResponse(&oaiResp, modelRequested)
	anthBody, err := json.Marshal(anthResp)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(anthBody); err != nil {
		slog.Warn("write Anthropic response", "error", err)
	}
	return handlerResult{
		InputTokens:          anthResp.Usage.InputTokens,
		OutputTokens:         anthResp.Usage.OutputTokens,
		CacheReadInputTokens: anthResp.Usage.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 anthBody,
	}
}

func (h *Handler) handleOpenAIProviderStreaming(w http.ResponseWriter, r *http.Request, resp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.ReverseSSEStream(r.Context(), w, resp.Stream, modelRequested)
	return streamingResult(resp, result, err)
}

// handleOpenAIProviderForcedStream handles the case where the client requested
// streaming but the provider config has stream:false. It fetches a non-streaming
// OpenAI response and synthesizes Anthropic SSE events for the client.
func (h *Handler) handleOpenAIProviderForcedStream(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
		return handlerResult{
			InputTokens:     resp.InputTokens,
			OutputTokens:    resp.OutputTokens,
			ReasoningTokens: resp.ReasoningTokens,
			Status:          resp.StatusCode,
			Body:            resp.Body,
			ErrDetails:      &s,
			IsStreaming:     true,
		}
	}

	// Parse the non-streaming OpenAI response.
	var oaiResp openai.ChatCompletionResponse
	if err := json.Unmarshal(resp.Body, &oaiResp); err != nil {
		s := fmt.Sprintf("failed to parse upstream response: %v", err)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return handlerResult{
			Status:      http.StatusBadGateway,
			Body:        resp.Body,
			ErrDetails:  &s,
			IsStreaming: true,
		}
	}

	// Translate to Anthropic format and synthesize SSE for the streaming client.
	anthResp := translate.OpenAIToAnthropicResponse(&oaiResp, modelRequested)
	result, err := SynthesizeAnthropicSSE(w, anthResp)

	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream synthesis error: %v", err)
		errDetails = &s
		slog.Warn("stream synthesis error", "error", err)
	}
	return handlerResult{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheCreationInputTokens: result.CacheCreationInputTokens,
		CacheReadInputTokens:     result.CacheReadInputTokens,
		ReasoningTokens:          resp.ReasoningTokens,
		Status:                   http.StatusOK,
		Body:                     result.Body,
		ErrDetails:               errDetails,
		IsStreaming:              true,
	}
}

// buildResponsesProviderRoute handles Anthropic-format requests that need to be
// sent to a Responses API upstream. It translates Anthropic→Responses on
// request and Responses→Anthropic on response.
func (h *Handler) buildResponsesProviderRoute(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}, mapping *config.DispatchTarget,
) (*routePlan, bool) {
	// Parse the full Anthropic request for translation.
	var anthReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthReq); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return nil, true
	}

	// Translate to Responses API format.
	anthReq.Model = mapping.UpstreamModel
	respReq, err := translate.AnthropicToResponses(&anthReq, mapping.UpstreamModel)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	respBody, err := json.Marshal(respReq)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        respBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: reqBody.Stream,
		},
		RequestBody: body,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleResponsesProviderNonStreaming(w, provResp, reqBody.Model)
			}
			if reqBody.Stream {
				return h.handleResponsesProviderStreaming(w, r, provResp, reqBody.Model)
			}
			return h.handleResponsesProviderNonStreaming(w, provResp, reqBody.Model)
		},
	}, false
}

func (h *Handler) handleResponsesProviderNonStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
		return handlerResult{
			InputTokens:     resp.InputTokens,
			OutputTokens:    resp.OutputTokens,
			ReasoningTokens: resp.ReasoningTokens,
			Status:          resp.StatusCode,
			Body:            resp.Body,
			ErrDetails:      &s,
		}
	}

	// Translate Responses API response to Anthropic format.
	anthResp := translate.ResponsesToAnthropicResponse(resp.Body, modelRequested)
	anthBody, err := json.Marshal(anthResp)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(anthBody); err != nil {
		slog.Warn("write Anthropic response", "error", err)
	}
	return handlerResult{
		InputTokens:          anthResp.Usage.InputTokens,
		OutputTokens:         anthResp.Usage.OutputTokens,
		CacheReadInputTokens: anthResp.Usage.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 anthBody,
	}
}

func (h *Handler) handleResponsesProviderStreaming(w http.ResponseWriter, r *http.Request, resp *provider.Response, modelRequested string) handlerResult {
	// The upstream (Responses API) doesn't send anthropic-beta, but the
	// translator can produce thinking content blocks. Inject the header so
	// Claude Code knows to process and render them.
	w.Header().Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	result, err := translate.ResponsesSSEToAnthropicSSE(r.Context(), w, resp.Stream, modelRequested)
	return streamingResult(resp, result, err)
}

// buildGeminiProviderRoute handles Anthropic-format requests that need to be
// sent to a native Gemini (Vertex AI) provider. It translates Anthropic→Gemini
// on request and Gemini→Anthropic on response.
func (h *Handler) buildGeminiProviderRoute(w http.ResponseWriter, r *http.Request,
	body []byte, reqBody *struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}, mapping *config.DispatchTarget,
) (*routePlan, bool) {
	var anthReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthReq); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return nil, true
	}

	anthReq.Model = mapping.UpstreamModel
	gemReq, err := translate.AnthropicToGeminiRequest(&anthReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	// Check if this provider forces non-streaming upstream calls.
	forceNonStream := providerForcesNonStream(h.cfg, mapping.Provider)

	gemBody, err := json.Marshal(gemReq)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        gemBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: reqBody.Stream && !forceNonStream,
		},
		RequestBody: body,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleGeminiProviderNonStreaming(w, provResp, reqBody.Model)
			}
			switch {
			case reqBody.Stream && forceNonStream:
				return h.handleGeminiProviderForcedStream(w, provResp, reqBody.Model)
			case reqBody.Stream:
				return h.handleGeminiProviderStreaming(w, provResp, reqBody.Model)
			default:
				return h.handleGeminiProviderNonStreaming(w, provResp, reqBody.Model)
			}
		},
	}, false
}

func (h *Handler) handleGeminiProviderNonStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
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

	anthResp := translate.GeminiToAnthropicResponse(resp.Body, modelRequested)
	anthBody, err := json.Marshal(anthResp)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		s := "Internal server error"
		return handlerResult{Status: http.StatusInternalServerError, ErrDetails: &s}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(anthBody); err != nil {
		slog.Warn("write Anthropic response", "error", err)
	}
	return handlerResult{
		InputTokens:          resp.InputTokens,
		OutputTokens:         resp.OutputTokens,
		CacheReadInputTokens: resp.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 anthBody,
	}
}

func (h *Handler) handleGeminiProviderStreaming(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.Stream == nil {
		return h.handleGeminiProviderForcedStream(w, resp, modelRequested)
	}

	result, err := translate.GeminiSSEToAnthropicSSE(w, resp.Stream, modelRequested)
	return streamingResult(resp, result, err)
}

// handleGeminiProviderForcedStream handles client streaming requests when the
// provider config has stream:false. It fetches a non-streaming Gemini response
// and synthesizes Anthropic SSE events for the client.
func (h *Handler) handleGeminiProviderForcedStream(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeAnthropicError(w, resp.StatusCode, "api_error", s)
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

	anthResp := translate.GeminiToAnthropicResponse(resp.Body, modelRequested)
	result, err := SynthesizeAnthropicSSE(w, anthResp)
	return synthesizedStreamResult(resp, result, err)
}

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/metrics"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	anthropic "github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/translate"
)

// OpenAIHandler is the proxy HTTP handler for OpenAI-compatible requests.
// It translates OpenAI Chat Completions requests to Anthropic format,
// dispatches them via the configured provider, and translates responses
// back to OpenAI format.
type OpenAIHandler struct {
	cfg           *config.Config
	providers     map[string]provider.Provider
	calculator    *pricing.Calculator
	logger        *AsyncLogger
	budgetChecker *BudgetChecker
}

// NewOpenAIHandler creates a new OpenAI-compatible proxy handler.
func NewOpenAIHandler(cfg *config.Config, providers map[string]provider.Provider, calc *pricing.Calculator, logger *AsyncLogger, bc *BudgetChecker) *OpenAIHandler {
	return &OpenAIHandler{
		cfg:           cfg,
		providers:     providers,
		calculator:    calc,
		logger:        logger,
		budgetChecker: bc,
	}
}

// ServeHTTP handles POST /v1/chat/completions requests.
func (h *OpenAIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	start := time.Now()

	body, ok := readRequestBodyWithLimit(w, r, h.cfg.ProxyMaxBodyBytes, "invalid_request_error", writeOpenAIError)
	if !ok {
		return
	}

	// Parse the OpenAI request.
	var oaiReq openai.ChatCompletionRequest
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
	keyPrefix := ""
	if pk != nil {
		proxyKeyID = pk.ID
		keyPrefix = pk.KeyPrefix
	}

	metrics.RecordActiveRequest("openai")
	defer metrics.FinishActiveRequest("openai")

	if h.budgetChecker != nil && proxyKeyID != "" {
		if violation, err := h.budgetChecker.Check(r.Context(), proxyKeyID); err != nil {
			slog.Warn("budget check error", "error", err)
		} else if violation != nil {
			writeOpenAIError(w, http.StatusTooManyRequests, "budget_exceeded",
				fmt.Sprintf("Budget exceeded: %s %s limit $%.2f, spent $%.2f, resets %s",
					violation.Scope, violation.Period, violation.LimitUSD, violation.SpendUSD,
					violation.ResetAt.In(h.budgetChecker.tz).Format(time.RFC3339)))
			return
		}
	}

	executeProxyPipeline(w, r, h.logger, chain, h.providers, pipelineSpec{
		SourceFormat: "openai",
		ProxyKeyID:   proxyKeyID,
		KeyPrefix:    keyPrefix,
		ModelRequest: oaiReq.Model,
		IsStreaming:  oaiReq.Stream,
		Start:        start,
		Calculator:   h.calculator,
	}, h.routeBuilders(w, r, &oaiReq, body),
		newPipelineCallbacks(w, writeOpenAIError, "api_error"))
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
			InputTokens:          resp.InputTokens,
			OutputTokens:         resp.OutputTokens,
			CacheReadInputTokens: resp.CacheReadInputTokens,
			ReasoningTokens:      resp.ReasoningTokens,
			Status:               resp.StatusCode,
			Body:                 resp.Body,
			ErrDetails:           &s,
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
	return streamingResult(resp, result, err)
}

func (h *OpenAIHandler) routeBuilders(w http.ResponseWriter, r *http.Request, oaiReq *openai.ChatCompletionRequest, rawBody []byte) map[string]routeBuilder {
	return map[string]routeBuilder{
		"anthropic": func(attempt chainAttempt) (*routePlan, bool) {
			mapping := attempt.Mapping
			oaiReqCopy := *oaiReq
			oaiReqCopy.Model = mapping.UpstreamModel
			anthReq, err := translate.OpenAIToAnthropic(&oaiReqCopy)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
				return nil, true
			}

			anthBody, err := json.Marshal(anthReq)
			if err != nil {
				writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
				return nil, true
			}

			return &routePlan{
				ProviderRequest: &provider.Request{
					Body:        anthBody,
					Headers:     r.Header.Clone(),
					Model:       mapping.UpstreamModel,
					IsStreaming: oaiReq.Stream,
				},
				RequestBody: rawBody,
				HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
					if provResp.StatusCode >= 400 {
						return h.handleOpenAINonStreaming(w, provResp, oaiReq.Model)
					}
					if oaiReq.Stream {
						return h.handleOpenAIStreaming(w, provResp, oaiReq.Model)
					}
					return h.handleOpenAINonStreaming(w, provResp, oaiReq.Model)
				},
			}, false
		},
		"openai": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildOpenAINativeRoute(w, r, oaiReq, rawBody, &attempt.Mapping)
		},
		"responses": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildResponsesRoute(w, r, oaiReq, rawBody, &attempt.Mapping)
		},
		"gemini": func(attempt chainAttempt) (*routePlan, bool) {
			return h.buildGeminiRoute(w, r, oaiReq, rawBody, &attempt.Mapping)
		},
	}
}

// buildOpenAINativeRoute handles requests to OpenAI-native providers
// without translating through Anthropic format.
func (h *OpenAIHandler) buildOpenAINativeRoute(w http.ResponseWriter, r *http.Request,
	oaiReq *openai.ChatCompletionRequest, rawBody []byte,
	mapping *config.DispatchTarget,
) (*routePlan, bool) {
	// Replace model name in the raw JSON body to preserve all original fields.
	var bodyMap map[string]any
	if err := json.Unmarshal(rawBody, &bodyMap); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body")
		return nil, true
	}
	bodyMap["model"] = mapping.UpstreamModel
	if oaiReq.Stream {
		if _, alreadySet := bodyMap["stream_options"]; !alreadySet {
			bodyMap["stream_options"] = map[string]bool{"include_usage": true}
		}
	}
	nativeBody, err := json.Marshal(bodyMap)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        nativeBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: oaiReq.Stream,
		},
		RequestBody: rawBody,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleOpenAINativeNonStreaming(w, provResp)
			}
			if oaiReq.Stream {
				return h.handleOpenAINativeStreaming(r.Context(), w, provResp)
			}
			return h.handleOpenAINativeNonStreaming(w, provResp)
		},
	}, false
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

func (h *OpenAIHandler) handleOpenAINativeStreaming(ctx context.Context, w http.ResponseWriter, resp *provider.Response) handlerResult {
	result, err := RelayOpenAISSEStream(ctx, w, resp.Stream)
	return streamingResult(resp, result, err)
}

// buildResponsesRoute handles CC requests that need to be sent to a Responses
// API upstream. Translates CC→Responses on request and Responses→CC on response.
func (h *OpenAIHandler) buildResponsesRoute(w http.ResponseWriter, r *http.Request,
	oaiReq *openai.ChatCompletionRequest, rawBody []byte,
	mapping *config.DispatchTarget,
) (*routePlan, bool) {
	// Translate CC to Responses API format.
	oaiReqCopy := *oaiReq
	oaiReqCopy.Model = mapping.UpstreamModel
	respReq, err := translate.OpenAIToResponses(&oaiReqCopy, mapping.UpstreamModel)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	respBody, err := json.Marshal(respReq)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        respBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: oaiReq.Stream,
		},
		RequestBody: rawBody,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleResponsesProviderNonStreamingToCC(w, provResp, oaiReq.Model)
			}
			if oaiReq.Stream {
				return h.handleResponsesProviderStreamingToCC(w, r, provResp, oaiReq.Model)
			}
			return h.handleResponsesProviderNonStreamingToCC(w, provResp, oaiReq.Model)
		},
	}, false
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

func (h *OpenAIHandler) handleResponsesProviderStreamingToCC(w http.ResponseWriter, r *http.Request, resp *provider.Response, modelRequested string) handlerResult {
	result, err := translate.ResponsesSSEToOpenAISSE(r.Context(), w, resp.Stream, modelRequested)
	return streamingResult(resp, result, err)
}

// buildGeminiRoute handles CC requests that need to be sent to a native Gemini
// (Vertex AI) provider. Translates CC→Gemini on request and Gemini→CC on response.
func (h *OpenAIHandler) buildGeminiRoute(w http.ResponseWriter, r *http.Request,
	oaiReq *openai.ChatCompletionRequest, rawBody []byte,
	mapping *config.DispatchTarget,
) (*routePlan, bool) {
	oaiReqCopy := *oaiReq
	oaiReqCopy.Model = mapping.UpstreamModel
	gemReq, err := translate.OpenAIToGeminiRequest(&oaiReqCopy)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Translation error: %s", err.Error()))
		return nil, true
	}

	gemBody, err := json.Marshal(gemReq)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Internal server error")
		return nil, true
	}

	forceNonStream := providerForcesNonStream(h.cfg, mapping.Provider)

	return &routePlan{
		ProviderRequest: &provider.Request{
			Body:        gemBody,
			Headers:     r.Header.Clone(),
			Model:       mapping.UpstreamModel,
			IsStreaming: oaiReq.Stream && !forceNonStream,
		},
		RequestBody: rawBody,
		HandleResponse: func(w http.ResponseWriter, provResp *provider.Response) handlerResult {
			if provResp.StatusCode >= 400 {
				return h.handleGeminiNonStreamingToCC(w, provResp, oaiReq.Model)
			}
			switch {
			case oaiReq.Stream && forceNonStream:
				return h.handleGeminiForcedStreamToCC(w, provResp, oaiReq.Model)
			case oaiReq.Stream:
				return h.handleGeminiStreamingToCC(w, provResp, oaiReq.Model)
			default:
				return h.handleGeminiNonStreamingToCC(w, provResp, oaiReq.Model)
			}
		},
	}, false
}

func (h *OpenAIHandler) handleGeminiNonStreamingToCC(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeOpenAIError(w, resp.StatusCode, "api_error", s)
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

	ccResp := translate.GeminiToOpenAIResponse(resp.Body, modelRequested)
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
		InputTokens:          resp.InputTokens,
		OutputTokens:         resp.OutputTokens,
		CacheReadInputTokens: resp.CacheReadInputTokens,
		ReasoningTokens:      resp.ReasoningTokens,
		Status:               http.StatusOK,
		Body:                 ccBody,
	}
}

func (h *OpenAIHandler) handleGeminiStreamingToCC(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.Stream == nil {
		return h.handleGeminiForcedStreamToCC(w, resp, modelRequested)
	}

	result, err := translate.GeminiSSEToOpenAISSE(w, resp.Stream, modelRequested)
	return streamingResult(resp, result, err)
}

func (h *OpenAIHandler) handleGeminiForcedStreamToCC(w http.ResponseWriter, resp *provider.Response, modelRequested string) handlerResult {
	if resp.StatusCode >= 400 {
		s := string(resp.Body)
		writeOpenAIError(w, resp.StatusCode, "api_error", s)
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

	ccResp := translate.GeminiToOpenAIResponse(resp.Body, modelRequested)
	result, err := SynthesizeOpenAISSE(w, ccResp)
	return synthesizedStreamResult(resp, result, err)
}

// writeOpenAIError writes an error response in OpenAI format.
func writeOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(openai.ErrorResponse{
		Error: openai.Error{
			Message: message,
			Type:    errType,
		},
	}); err != nil {
		slog.Warn("write OpenAI error", "error", err)
	}
}

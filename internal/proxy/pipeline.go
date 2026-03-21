// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/seckatie/glitchgate/internal/circuitbreaker"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
)

type responseAdapter func(http.ResponseWriter, *provider.Response) handlerResult

// baseHandler holds the shared dependencies used by all proxy handler types.
type baseHandler struct {
	cfg           *config.Config
	providers     map[string]provider.Provider
	calculator    *pricing.Calculator
	logger        *AsyncLogger
	budgetChecker *BudgetChecker
	breakers      *circuitbreaker.Registry
	modelChecker  *ModelChecker
}

// providerForcesNonStream returns true when the provider config explicitly
// disables streaming (stream: false). All handler types use this to decide
// whether to synthesize SSE from a non-streaming upstream response.
func providerForcesNonStream(cfg *config.Config, providerName string) bool {
	provCfg, err := cfg.FindProvider(providerName)
	return err == nil && provCfg.Stream != nil && !*provCfg.Stream
}

type routePlan struct {
	ProviderRequest *provider.Request
	RequestBody     []byte
	HandleResponse  responseAdapter
}

type routeBuilder func(chainAttempt) (*routePlan, bool)

// pipelineCallbacks holds the four error callbacks that executeProxyPipeline
// needs, built from a format-specific error writer.
type pipelineCallbacks struct {
	onMissingProvider   func(string)
	onUnsupportedFormat func(provider.Provider) bool
	onNetworkError      func()
	onExhaustedError    func(int)
}

// newPipelineCallbacks builds the four error callbacks parameterised on the
// format-specific error writer (writeAnthropicError, writeOpenAIError, etc.).
// serverErrType is the error type used for server-side failures (e.g.
// "api_error" for Anthropic/OpenAI, "server_error" for Responses API).
func newPipelineCallbacks(w http.ResponseWriter, writeErr errorWriter, serverErrType string) pipelineCallbacks {
	return pipelineCallbacks{
		onMissingProvider: func(name string) {
			writeErr(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Provider not configured: %s", name))
		},
		onUnsupportedFormat: func(prov provider.Provider) bool {
			writeErr(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("Unsupported upstream format %q for provider %s", prov.APIFormat(), prov.Name()))
			return true
		},
		onNetworkError: func() {
			writeErr(w, http.StatusBadGateway, serverErrType, "Failed to reach upstream provider")
		},
		onExhaustedError: func(status int) {
			writeErr(w, status, serverErrType, "All upstream providers failed")
		},
	}
}

type pipelineSpec struct {
	SourceFormat string
	ProxyKeyID   string
	KeyPrefix    string
	ModelRequest string
	IsStreaming  bool
	Start        time.Time
	Calculator   *pricing.Calculator
}

type chainAttempt struct {
	Mapping          config.DispatchTarget
	Provider         provider.Provider
	AttemptCount     int64
	HasMoreFallbacks bool
}

type providerAttempt struct {
	SourceFormat     string
	ProxyKeyID       string
	KeyPrefix        string
	ModelRequested   string
	ModelUpstream    string
	RequestBody      []byte
	IsStreaming      bool
	AttemptCount     int64
	HasMoreFallbacks bool
	Start            time.Time
}

func executeFallbackChain(
	chain []config.DispatchTarget,
	providers map[string]provider.Provider,
	breakers *circuitbreaker.Registry,
	onMissingProvider func(string),
	handleAttempt func(chainAttempt) bool,
) {
	allSkipped := true
	for attempt, mapping := range chain {
		prov, ok := providers[mapping.Provider]
		if !ok {
			onMissingProvider(mapping.Provider)
			return
		}

		// Consult circuit breaker — skip providers whose circuit is open.
		if breakers != nil {
			cb := breakers.Get(mapping.Provider)
			if !cb.Allow() {
				slog.Info("circuit breaker skipping provider",
					"provider", mapping.Provider,
					"state", cb.CurrentState().String())
				continue
			}
		}

		allSkipped = false
		if handleAttempt(chainAttempt{
			Mapping:          mapping,
			Provider:         prov,
			AttemptCount:     int64(attempt + 1),
			HasMoreFallbacks: attempt < len(chain)-1,
		}) {
			return
		}
	}
	// If every provider was skipped by the circuit breaker, the caller
	// will fall through without writing a response. We need to handle
	// that case here.
	if allSkipped && len(chain) > 0 {
		return
	}
}

func executeProviderAttempt(
	r *http.Request,
	logger *AsyncLogger,
	prov provider.Provider,
	provReq *provider.Request,
	attempt providerAttempt,
	calc *pricing.Calculator,
	cb *circuitbreaker.Breaker,
	writeNetworkError func(),
	writeExhaustedError func(int),
	handleResponse func(*provider.Response) handlerResult,
) bool {
	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		if cb != nil {
			cb.RecordFailure()
		}
		if attempt.HasMoreFallbacks {
			return false
		}
		latency := time.Since(attempt.Start).Milliseconds()
		errMsg := err.Error()
		logger.logEntry(attempt.ProxyKeyID, attempt.KeyPrefix, attempt.SourceFormat, prov.Name(), attempt.ModelRequested, attempt.ModelUpstream, "",
			latency, attempt.RequestBody, attempt.AttemptCount, handlerResult{
				Status: http.StatusBadGateway, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: attempt.IsStreaming,
			}, calc)
		writeNetworkError()
		return true
	}

	if isFallbackStatus(provResp.StatusCode) {
		if cb != nil {
			cb.RecordFailure()
		}
		if provResp.Stream != nil {
			_ = provResp.Stream.Close()
		}
		if attempt.HasMoreFallbacks {
			return false
		}
		latency := time.Since(attempt.Start).Milliseconds()
		errMsg := fmt.Sprintf("all %d fallback entries exhausted; last status %d", attempt.AttemptCount, provResp.StatusCode)
		logger.logEntry(attempt.ProxyKeyID, attempt.KeyPrefix, attempt.SourceFormat, prov.Name(), attempt.ModelRequested, attempt.ModelUpstream, "",
			latency, attempt.RequestBody, attempt.AttemptCount, handlerResult{
				Status: provResp.StatusCode, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: attempt.IsStreaming,
			}, calc)
		writeExhaustedError(provResp.StatusCode)
		return true
	}

	if cb != nil {
		cb.RecordSuccess()
	}
	result := handleResponse(provResp)
	latency := time.Since(attempt.Start).Milliseconds()
	logger.logEntry(attempt.ProxyKeyID, attempt.KeyPrefix, attempt.SourceFormat, prov.Name(), attempt.ModelRequested, attempt.ModelUpstream, "",
		latency, attempt.RequestBody, attempt.AttemptCount, result, calc)
	return true
}

func executeProxyPipeline(
	w http.ResponseWriter,
	r *http.Request,
	logger *AsyncLogger,
	chain []config.DispatchTarget,
	providers map[string]provider.Provider,
	breakers *circuitbreaker.Registry,
	spec pipelineSpec,
	routes map[string]routeBuilder,
	cbs pipelineCallbacks,
) {
	anyAttempted := false
	executeFallbackChain(chain, providers, breakers, cbs.onMissingProvider, func(attempt chainAttempt) bool {
		anyAttempted = true
		buildRoute, ok := routes[attempt.Provider.APIFormat()]
		if !ok {
			return cbs.onUnsupportedFormat(attempt.Provider)
		}

		plan, stop := buildRoute(attempt)
		if stop || plan == nil {
			return true
		}

		var cb *circuitbreaker.Breaker
		if breakers != nil {
			cb = breakers.Get(attempt.Provider.Name())
		}

		return executeProviderAttempt(r, logger, attempt.Provider, plan.ProviderRequest, providerAttempt{
			SourceFormat:     spec.SourceFormat,
			ProxyKeyID:       spec.ProxyKeyID,
			KeyPrefix:        spec.KeyPrefix,
			ModelRequested:   spec.ModelRequest,
			ModelUpstream:    attempt.Mapping.UpstreamModel,
			RequestBody:      plan.RequestBody,
			IsStreaming:      spec.IsStreaming,
			AttemptCount:     attempt.AttemptCount,
			HasMoreFallbacks: attempt.HasMoreFallbacks,
			Start:            spec.Start,
		}, spec.Calculator, cb, cbs.onNetworkError, cbs.onExhaustedError, func(provResp *provider.Response) handlerResult {
			return plan.HandleResponse(w, provResp)
		})
	})
	if !anyAttempted && len(chain) > 0 {
		slog.Warn("all providers skipped by circuit breaker", "model", spec.ModelRequest)
		cbs.onExhaustedError(http.StatusServiceUnavailable)
	}
}

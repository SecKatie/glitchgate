// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"fmt"
	"net/http"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
)

type responseAdapter func(http.ResponseWriter, *provider.Response) handlerResult

type routePlan struct {
	ProviderRequest *provider.Request
	RequestBody     []byte
	HandleResponse  responseAdapter
}

type routeBuilder func(chainAttempt) (*routePlan, bool)

type pipelineSpec struct {
	SourceFormat string
	ProxyKeyID   string
	ModelRequest string
	IsStreaming  bool
	Start        time.Time
}

type chainAttempt struct {
	Mapping          config.ModelMapping
	Provider         provider.Provider
	AttemptCount     int64
	HasMoreFallbacks bool
}

type providerAttempt struct {
	SourceFormat     string
	ProxyKeyID       string
	ModelRequested   string
	ModelUpstream    string
	RequestBody      []byte
	IsStreaming      bool
	AttemptCount     int64
	HasMoreFallbacks bool
	Start            time.Time
}

func executeFallbackChain(
	chain []config.ModelMapping,
	providers map[string]provider.Provider,
	onMissingProvider func(string),
	handleAttempt func(chainAttempt) bool,
) {
	for attempt, mapping := range chain {
		prov, ok := providers[mapping.Provider]
		if !ok {
			onMissingProvider(mapping.Provider)
			return
		}
		if handleAttempt(chainAttempt{
			Mapping:          mapping,
			Provider:         prov,
			AttemptCount:     int64(attempt + 1),
			HasMoreFallbacks: attempt < len(chain)-1,
		}) {
			return
		}
	}
}

func executeProviderAttempt(
	r *http.Request,
	logger *AsyncLogger,
	prov provider.Provider,
	provReq *provider.Request,
	attempt providerAttempt,
	writeNetworkError func(),
	writeExhaustedError func(),
	handleResponse func(*provider.Response) handlerResult,
) bool {
	provResp, err := prov.SendRequest(r.Context(), provReq)
	if err != nil {
		if attempt.HasMoreFallbacks {
			return false
		}
		latency := time.Since(attempt.Start).Milliseconds()
		errMsg := err.Error()
		logger.logEntry(attempt.ProxyKeyID, attempt.SourceFormat, providerNameFor(prov), attempt.ModelRequested, attempt.ModelUpstream, "",
			latency, attempt.RequestBody, attempt.AttemptCount, handlerResult{
				Status: http.StatusBadGateway, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: attempt.IsStreaming,
			})
		writeNetworkError()
		return true
	}

	if isFallbackStatus(provResp.StatusCode) {
		if provResp.Stream != nil {
			_ = provResp.Stream.Close()
		}
		if attempt.HasMoreFallbacks {
			return false
		}
		latency := time.Since(attempt.Start).Milliseconds()
		errMsg := fmt.Sprintf("all %d fallback entries exhausted; last status %d", attempt.AttemptCount, provResp.StatusCode)
		logger.logEntry(attempt.ProxyKeyID, attempt.SourceFormat, providerNameFor(prov), attempt.ModelRequested, attempt.ModelUpstream, "",
			latency, attempt.RequestBody, attempt.AttemptCount, handlerResult{
				Status: http.StatusServiceUnavailable, Body: []byte(errMsg),
				ErrDetails: &errMsg, IsStreaming: attempt.IsStreaming,
			})
		writeExhaustedError()
		return true
	}

	result := handleResponse(provResp)
	latency := time.Since(attempt.Start).Milliseconds()
	logger.logEntry(attempt.ProxyKeyID, attempt.SourceFormat, providerNameFor(prov), attempt.ModelRequested, attempt.ModelUpstream, "",
		latency, attempt.RequestBody, attempt.AttemptCount, result)
	return true
}

func executeProxyPipeline(
	w http.ResponseWriter,
	r *http.Request,
	logger *AsyncLogger,
	chain []config.ModelMapping,
	providers map[string]provider.Provider,
	spec pipelineSpec,
	routes map[string]routeBuilder,
	onMissingProvider func(string),
	onUnsupportedFormat func(provider.Provider) bool,
	writeNetworkError func(),
	writeExhaustedError func(),
) {
	executeFallbackChain(chain, providers, onMissingProvider, func(attempt chainAttempt) bool {
		buildRoute, ok := routes[attempt.Provider.APIFormat()]
		if !ok {
			return onUnsupportedFormat(attempt.Provider)
		}

		plan, stop := buildRoute(attempt)
		if stop || plan == nil {
			return true
		}

		return executeProviderAttempt(r, logger, attempt.Provider, plan.ProviderRequest, providerAttempt{
			SourceFormat:     spec.SourceFormat,
			ProxyKeyID:       spec.ProxyKeyID,
			ModelRequested:   spec.ModelRequest,
			ModelUpstream:    attempt.Mapping.UpstreamModel,
			RequestBody:      plan.RequestBody,
			IsStreaming:      spec.IsStreaming,
			AttemptCount:     attempt.AttemptCount,
			HasMoreFallbacks: attempt.HasMoreFallbacks,
			Start:            spec.Start,
		}, writeNetworkError, writeExhaustedError, func(provResp *provider.Response) handlerResult {
			return plan.HandleResponse(w, provResp)
		})
	})
}

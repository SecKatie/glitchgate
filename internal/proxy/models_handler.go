package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
)

// modelsCreatedAt is the timestamp used for model "created" field.
// In production this would be the config load time; using a fixed value
// for consistency in responses.
var modelsCreatedAt = time.Now().Unix()

// ModelsHandler is the HTTP handler for OpenAI-compatible /v1/models endpoint.
type ModelsHandler struct {
	cfg        *config.Config
	calculator *pricing.Calculator
	logger     *AsyncLogger
}

// NewModelsHandler creates a new models handler.
func NewModelsHandler(cfg *config.Config, calc *pricing.Calculator, logger *AsyncLogger) *ModelsHandler {
	return &ModelsHandler{
		cfg:        cfg,
		calculator: calc,
		logger:     logger,
	}
}

// ServeHTTP handles GET /v1/models requests.
func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Use config load time as surrogate for "created" timestamp.
	created := modelsCreatedAt

	var models []ModelResponse
	for _, m := range h.cfg.ModelList {
		// Skip wildcard entries.
		if m.IsWildcard() {
			continue
		}

		// Determine owned_by (provider or "virtual" for fallback models).
		ownedBy := m.Provider
		if len(m.Fallbacks) > 0 {
			ownedBy = "virtual"
		}

		// Build capabilities.
		capabilities := ModelCapabilities{
			Streaming: true, // All models support streaming in this proxy.
		}
		if len(m.Fallbacks) > 0 {
			capabilities.Fallbacks = m.Fallbacks
		}

		// Get pricing from calculator if we have provider and upstream model.
		var pricingInfo *ModelPricing
		if m.Provider != "" && m.UpstreamModel != "" {
			if entry, ok := h.calculator.Lookup(m.Provider, m.UpstreamModel); ok {
				pricingInfo = &ModelPricing{
					InputTokenCost:  entry.InputPerMillion,
					OutputTokenCost: entry.OutputPerMillion,
				}
				// Add cache pricing if available.
				if entry.CacheWritePerMillion > 0 || entry.CacheReadPerMillion > 0 {
					pricingInfo.CacheWriteTokenCost = entry.CacheWritePerMillion
					pricingInfo.CacheReadTokenCost = entry.CacheReadPerMillion
				}
			}
		}

		// Override with config metadata if present.
		if m.Metadata != nil && pricingInfo == nil {
			pricingInfo = &ModelPricing{
				InputTokenCost:  m.Metadata.InputTokenCost,
				OutputTokenCost: m.Metadata.OutputTokenCost,
			}
			if m.Metadata.CacheWriteCost > 0 {
				pricingInfo.CacheWriteTokenCost = m.Metadata.CacheWriteCost
			}
			if m.Metadata.CacheReadCost > 0 {
				pricingInfo.CacheReadTokenCost = m.Metadata.CacheReadCost
			}
		}

		models = append(models, ModelResponse{
			ID:           m.ModelName,
			Object:       "model",
			Created:      created,
			OwnedBy:      ownedBy,
			Capabilities: capabilities,
			Pricing:      pricingInfo,
		})
	}

	response := ModelsListResponse{
		Object: "list",
		Data:   models,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Warn("encode models response", "error", err)
	}
}

// ModelsListResponse is the top-level response for /v1/models.
type ModelsListResponse struct {
	Object string          `json:"object"`
	Data   []ModelResponse `json:"data"`
}

// ModelResponse represents a single model in the response.
type ModelResponse struct {
	ID           string            `json:"id"`
	Object       string            `json:"object"`
	Created      int64             `json:"created"`
	OwnedBy      string            `json:"owned_by"`
	Capabilities ModelCapabilities `json:"capabilities"`
	Pricing      *ModelPricing     `json:"pricing,omitempty"`
}

// ModelCapabilities describes what a model can do.
type ModelCapabilities struct {
	Streaming bool     `json:"streaming"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

// ModelPricing contains pricing information for a model.
type ModelPricing struct {
	InputTokenCost      float64 `json:"input_token_cost,omitempty"`
	OutputTokenCost     float64 `json:"output_token_cost,omitempty"`
	CacheWriteTokenCost float64 `json:"cache_write_token_cost,omitempty"`
	CacheReadTokenCost  float64 `json:"cache_read_token_cost,omitempty"`
}

// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

// ---------------------------------------------------------------------------
// Stub store for handler tests
// ---------------------------------------------------------------------------

// stubModelStore is a minimal HandlersStore stub that only implements
// model-related methods. Any other method panics — tests should not call them.
type stubModelStore struct {
	HandlersStore // embed to satisfy interface; other methods panic
	summary       *store.ModelUsageSummary
	err           error
}

func (s *stubModelStore) GetModelUsageSummary(_ context.Context, _ string) (*store.ModelUsageSummary, error) {
	return s.summary, s.err
}

func (s *stubModelStore) GetModelCostPricingGroups(_ context.Context, _ string) ([]store.CostPricingGroup, error) {
	return nil, nil
}

func (s *stubModelStore) GetAllModelUsageSummaries(_ context.Context) (map[string]*store.ModelUsageSummary, error) {
	return map[string]*store.ModelUsageSummary{}, nil
}

func (s *stubModelStore) ListDistinctModels(_ context.Context) ([]string, error) {
	return nil, nil
}

func (s *stubModelStore) GetModelLatencyTimeseries(_ context.Context, _ string) ([]store.ModelLatencyTimeseriesEntry, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeTestCalc(provKey, model string, entry pricing.Entry) *pricing.Calculator {
	return pricing.NewCalculator(map[string]pricing.Entry{
		provKey + "/" + model: entry,
	})
}

func testProviders() []config.ProviderConfig {
	return []config.ProviderConfig{
		{Name: "anthropic", Type: "anthropic", BaseURL: "https://api.anthropic.com"},
		{Name: "copilot", Type: "github_copilot", BaseURL: ""},
	}
}

func testProviderMap() map[string]config.ProviderConfig {
	pm := make(map[string]config.ProviderConfig)
	for _, p := range testProviders() {
		pm[p.Name] = p
	}
	return pm
}

// ---------------------------------------------------------------------------
// T009: buildModelList unit tests
// ---------------------------------------------------------------------------

func TestBuildModelList(t *testing.T) {
	providerName := "anthropic"
	knownEntry := pricing.Entry{
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
	}
	calc := makeTestCalc(providerName, "claude-sonnet-4-20250514", knownEntry)
	emptyCalc := pricing.NewCalculator(map[string]pricing.Entry{})
	providerMap := testProviderMap()

	t.Run("direct model with known pricing", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		items := buildModelList(models, providerMap, calc)
		require.Len(t, items, 1)
		item := items[0]
		require.Equal(t, "claude-sonnet", item.ModelName)
		require.Equal(t, "anthropic", item.ProviderName)
		require.Equal(t, "anthropic", item.ProviderType)
		require.Equal(t, "claude-sonnet-4-20250514", item.UpstreamModel)
		require.False(t, item.IsVirtual)
		require.False(t, item.IsWildcard)
		require.True(t, item.HasPricing)
		require.NotNil(t, item.Pricing)
		require.Equal(t, knownEntry.InputPerMillion, item.Pricing.InputPerMillion)
		require.Equal(t, "claude-sonnet", item.EncodedName)
	})

	t.Run("direct model with unknown pricing", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "unknown-model", Provider: "anthropic", UpstreamModel: "unknown-4-x"},
		}
		items := buildModelList(models, providerMap, emptyCalc)
		require.Len(t, items, 1)
		require.False(t, items[0].HasPricing)
		require.Nil(t, items[0].Pricing)
	})

	t.Run("virtual model with fallbacks", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "fast", Fallbacks: []string{"claude-haiku", "claude-sonnet"}},
		}
		items := buildModelList(models, providerMap, calc)
		require.Len(t, items, 1)
		item := items[0]
		require.True(t, item.IsVirtual)
		require.False(t, item.HasPricing)
		require.Equal(t, []string{"claude-haiku", "claude-sonnet"}, item.Fallbacks)
		require.Empty(t, item.ProviderName)
		require.Empty(t, item.UpstreamModel)
	})

	t.Run("wildcard entry", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "gc/*", Provider: "copilot"},
		}
		items := buildModelList(models, providerMap, emptyCalc)
		require.Len(t, items, 1)
		require.True(t, items[0].IsWildcard)
		require.Equal(t, "gc%2F%2A", items[0].EncodedName)
	})

	t.Run("model name with slash is encoded correctly", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "gc/claude-sonnet", Provider: "copilot", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		items := buildModelList(models, providerMap, emptyCalc)
		require.Len(t, items, 1)
		require.Equal(t, "gc%2Fclaude-sonnet", items[0].EncodedName)
	})

	t.Run("metadata pricing override reflected in Lookup result", func(t *testing.T) {
		overrideEntry := pricing.Entry{
			InputPerMillion:  1.00,
			OutputPerMillion: 5.00,
		}
		overrideCalc := makeTestCalc(providerName, "claude-sonnet-4-20250514", overrideEntry)
		models := []config.ModelMapping{
			{
				ModelName:     "claude-sonnet",
				Provider:      "anthropic",
				UpstreamModel: "claude-sonnet-4-20250514",
				Metadata:      &config.ModelMetadata{InputTokenCost: 1.00, OutputTokenCost: 5.00},
			},
		}
		items := buildModelList(models, providerMap, overrideCalc)
		require.Len(t, items, 1)
		require.True(t, items[0].HasPricing)
		require.InDelta(t, 1.00, items[0].Pricing.InputPerMillion, 0.001)
		require.InDelta(t, 5.00, items[0].Pricing.OutputPerMillion, 0.001)
	})
}

// ---------------------------------------------------------------------------
// T012: ModelsPage handler tests
// ---------------------------------------------------------------------------

func TestModelsPage(t *testing.T) {
	providerName := "anthropic"
	knownEntry := pricing.Entry{InputPerMillion: 3.00, OutputPerMillion: 15.00}
	calc := makeTestCalc(providerName, "claude-sonnet-4-20250514", knownEntry)
	providerMap := testProviderMap()
	templates := ParseTemplates(time.UTC)
	stub := &stubModelStore{summary: &store.ModelUsageSummary{}}

	t.Run("200 OK for valid config", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		h := &Handlers{
			store:       stub,
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/models", nil)
		rec := httptest.NewRecorder()
		h.ModelsPage(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("table rows contain expected model names", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
			{ModelName: "claude-haiku", Provider: "anthropic", UpstreamModel: "claude-haiku-4-5-20251001"},
		}
		h := &Handlers{
			store:       stub,
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/models", nil)
		rec := httptest.NewRecorder()
		h.ModelsPage(rec, req)
		body := rec.Body.String()
		require.Contains(t, body, "claude-sonnet")
		require.Contains(t, body, "claude-haiku")
		require.Contains(t, body, "claude-sonnet-4-20250514")
		require.Contains(t, body, ">Copy</button>")
	})

	t.Run("Virtual label for fallback model", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "fast", Fallbacks: []string{"claude-haiku", "claude-sonnet"}},
		}
		h := &Handlers{
			store:       stub,
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/models", nil)
		rec := httptest.NewRecorder()
		h.ModelsPage(rec, req)
		body := rec.Body.String()
		require.Contains(t, body, "Virtual")
	})

	t.Run("dash shown when pricing unknown", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "unknown-model", Provider: "anthropic", UpstreamModel: "no-such-model"},
		}
		h := &Handlers{
			store:       stub,
			calc:        pricing.NewCalculator(map[string]pricing.Entry{}),
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/models", nil)
		rec := httptest.NewRecorder()
		h.ModelsPage(rec, req)
		body := rec.Body.String()
		require.Contains(t, body, "&mdash;") // em dash for unknown pricing
	})
}

// ---------------------------------------------------------------------------
// T018: ModelDetailPage handler tests
// ---------------------------------------------------------------------------

func TestModelDetailPage(t *testing.T) {
	providerName := "anthropic"
	knownEntry := pricing.Entry{InputPerMillion: 3.00, OutputPerMillion: 15.00}
	calc := makeTestCalc(providerName, "claude-sonnet-4-20250514", knownEntry)
	providerMap := testProviderMap()
	templates := ParseTemplates(time.UTC)
	zeroUsage := &store.ModelUsageSummary{}

	makeRequest := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rctx := chi.NewRouteContext()
		// chi wildcard param is the path after /ui/models/
		paramVal := strings.TrimPrefix(path, "/ui/models/")
		rctx.URLParams.Add("*", paramVal)
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}

	t.Run("200 for known direct model", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		h := &Handlers{
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
			store:       &stubModelStore{summary: zeroUsage},
		}
		rec := httptest.NewRecorder()
		h.ModelDetailPage(rec, makeRequest("/ui/models/claude-sonnet"))
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		require.Contains(t, body, "claude-sonnet")
		require.Contains(t, body, ">Copy</button>")
		require.Contains(t, body, "/v1/responses")
	})

	t.Run("200 for virtual model with fallbacks rendered", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "fast", Fallbacks: []string{"claude-haiku", "claude-sonnet"}},
		}
		h := &Handlers{
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
			store:       &stubModelStore{summary: zeroUsage},
		}
		rec := httptest.NewRecorder()
		h.ModelDetailPage(rec, makeRequest("/ui/models/fast"))
		body := rec.Body.String()
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, body, "claude-haiku")
		require.Contains(t, body, "claude-sonnet")
	})

	t.Run("404 for unknown model name", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		h := &Handlers{
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
			store:       &stubModelStore{summary: zeroUsage},
		}
		rec := httptest.NewRecorder()
		h.ModelDetailPage(rec, makeRequest("/ui/models/does-not-exist"))
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("YOUR_PROXY_KEY placeholder appears in response", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		}
		h := &Handlers{
			calc:        calc,
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
			store:       &stubModelStore{summary: zeroUsage},
		}
		rec := httptest.NewRecorder()
		h.ModelDetailPage(rec, makeRequest("/ui/models/claude-sonnet"))
		require.Contains(t, rec.Body.String(), "YOUR_PROXY_KEY")
	})

	t.Run("unknown pricing shows placeholder cards", func(t *testing.T) {
		models := []config.ModelMapping{
			{ModelName: "unknown-model", Provider: "anthropic", UpstreamModel: "no-such-model"},
		}
		h := &Handlers{
			calc:        pricing.NewCalculator(map[string]pricing.Entry{}),
			providerMap: providerMap,
			cfg:         &config.Config{ModelList: models},
			templates:   templates,
			store:       &stubModelStore{summary: zeroUsage},
		}
		rec := httptest.NewRecorder()
		h.ModelDetailPage(rec, makeRequest("/ui/models/unknown-model"))
		body := rec.Body.String()
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, body, `class="stat-value"><span class="placeholder-value">&mdash;</span></div>`)
		require.Contains(t, body, `class="pricing-card-value"><span class="placeholder-value">&mdash;</span></div>`)
		require.NotContains(t, body, "No pricing data available for this model.")
	})
}

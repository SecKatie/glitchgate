package proxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
	"github.com/stretchr/testify/require"
)

// modelsTestHarness bundles resources for models handler tests.
type modelsTestHarness struct {
	store     *store.SQLiteStore
	logger    *proxy.AsyncLogger
	handler   *proxy.ModelsHandler
	cfg       *config.Config
	apiKey    string
	keyID     string
	closeOnce sync.Once
}

func (h *modelsTestHarness) closeLogger() {
	h.closeOnce.Do(func() {
		h.logger.Close()
	})
}

func newModelsTestHarness(t *testing.T) *modelsTestHarness {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.Migrate(context.Background())
	require.NoError(t, err)

	plaintext, hash, prefix, err := auth.GenerateKey()
	require.NoError(t, err)

	keyID := "test-models-key-id"
	err = st.CreateProxyKey(context.Background(), keyID, hash, prefix, "test-models-key")
	require.NoError(t, err)

	cfg := &config.Config{
		MasterKey: "test-master-key",
		Listen:    ":4000",
		ModelList: []config.ModelMapping{
			{
				ModelName:     "claude-sonnet-4-6",
				Provider:      "anthropic",
				UpstreamModel: "claude-sonnet-4-6-20251001",
			},
			{
				ModelName:     "gpt-4o",
				Provider:      "openai",
				UpstreamModel: "gpt-4o",
			},
			{
				ModelName: "smart-model",
				Fallbacks: []string{
					"claude-sonnet-4-6",
					"gpt-4o",
				},
			},
			{
				ModelName: "chatgpt/*", // wildcard - should be excluded
				Provider:  "openai",
			},
		},
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"anthropic/claude-sonnet-4-6-20251001": {
			InputPerMillion:  3.0,
			OutputPerMillion: 15.0,
		},
		"openai/gpt-4o": {
			InputPerMillion:  2.5,
			OutputPerMillion: 10.0,
		},
	})
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewModelsHandler(cfg, calc, logger)

	h := &modelsTestHarness{
		store:   st,
		logger:  logger,
		handler: handler,
		cfg:     cfg,
		apiKey:  plaintext,
		keyID:   keyID,
	}
	t.Cleanup(func() { h.closeLogger() })
	return h
}

func (h *modelsTestHarness) buildAuthenticatedRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)

	pk, err := h.store.GetActiveProxyKeyByPrefix(context.Background(), h.apiKey[:12])
	require.NoError(t, err)
	ctx := proxy.ContextWithProxyKey(req.Context(), pk)
	return req.WithContext(ctx)
}

func TestModelsHandler_ReturnsList(t *testing.T) {
	h := newModelsTestHarness(t)

	req := h.buildAuthenticatedRequest(t, http.MethodGet, "/v1/models")
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp proxy.ModelsListResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Equal(t, "list", resp.Object)
	require.Len(t, resp.Data, 3) // excludes wildcard

	// Check first model (claude-sonnet-4-6)
	claudeModel := resp.Data[0]
	require.Equal(t, "claude-sonnet-4-6", claudeModel.ID)
	require.Equal(t, "model", claudeModel.Object)
	require.Equal(t, "anthropic", claudeModel.OwnedBy)
	require.True(t, claudeModel.Capabilities.Streaming)
	require.Nil(t, claudeModel.Capabilities.Fallbacks)
	require.NotNil(t, claudeModel.Pricing)
	require.Equal(t, 3.0, claudeModel.Pricing.InputTokenCost)
	require.Equal(t, 15.0, claudeModel.Pricing.OutputTokenCost)

	// Check second model (gpt-4o)
	gptModel := resp.Data[1]
	require.Equal(t, "gpt-4o", gptModel.ID)
	require.Equal(t, "openai", gptModel.OwnedBy)
	require.NotNil(t, gptModel.Pricing)
	require.Equal(t, 2.5, gptModel.Pricing.InputTokenCost)
	require.Equal(t, 10.0, gptModel.Pricing.OutputTokenCost)

	// Check third model (smart-model - virtual with fallbacks)
	virtualModel := resp.Data[2]
	require.Equal(t, "smart-model", virtualModel.ID)
	require.Equal(t, "virtual", virtualModel.OwnedBy)
	require.NotNil(t, virtualModel.Capabilities.Fallbacks)
	require.Equal(t, []string{"claude-sonnet-4-6", "gpt-4o"}, virtualModel.Capabilities.Fallbacks)
}

func TestModelsHandler_ExcludesWildcards(t *testing.T) {
	h := newModelsTestHarness(t)

	req := h.buildAuthenticatedRequest(t, http.MethodGet, "/v1/models")
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, req)

	var resp proxy.ModelsListResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Verify no wildcard models in the list
	for _, m := range resp.Data {
		require.False(t, strings.HasSuffix(m.ID, "/*"), "wildcard model should be excluded: %s", m.ID)
	}
}

func TestModelsHandler_MethodNotAllowed(t *testing.T) {
	h := newModelsTestHarness(t)

	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/models")
	w := httptest.NewRecorder()

	h.handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Should return an OpenAI-style error
	errorObj, ok := resp["error"].(map[string]interface{})
	require.True(t, ok, "response should contain error object")
	require.Equal(t, "invalid_request_error", errorObj["type"])
	require.Equal(t, "Method not allowed", errorObj["message"])
}

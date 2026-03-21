package proxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	openaiProv "github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
)

// responsesTestHarness bundles resources for Responses API handler tests.
type responsesTestHarness struct {
	store     *store.SQLiteStore
	logger    *proxy.AsyncLogger
	handler   *proxy.ResponsesHandler
	cfg       *config.Config
	providers map[string]provider.Provider
	apiKey    string
	keyID     string
	closeOnce sync.Once
}

// closeLogger drains the async logger exactly once, safe to call multiple times.
func (h *responsesTestHarness) closeLogger() {
	h.closeOnce.Do(func() {
		h.logger.Close()
	})
}

// newResponsesTestHarness creates a fully wired test harness with an OpenAI Responses API provider.
func newResponsesTestHarness(t *testing.T, upstreamURL string) *responsesTestHarness {
	t.Helper()

	st := cloneTestDB(t)
	plaintext := templateKey.Plaintext
	keyID := templateKey.ID

	cfg := &config.Config{
		MasterKey: "test-master-key",
		Listen:    ":4000",
		Providers: []config.ProviderConfig{
			{
				Name:     "openai-resp",
				Type:     "openai_responses",
				BaseURL:  upstreamURL,
				AuthMode: "proxy_key",
				APIKey:   "test-key",
			},
		},
		ModelList: []config.ModelMapping{
			{
				ModelName:     "gpt-4o",
				Provider:      "openai-resp",
				UpstreamModel: "gpt-4o",
			},
		},
	}

	openaiClient, err := openaiProv.NewClient("openai-resp", upstreamURL, "proxy_key", "test-key", openaiProv.APITypeResponses)
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"openai-resp": openaiClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewResponsesHandler(cfg, providers, calc, logger, nil, nil, nil)

	h := &responsesTestHarness{
		store:     st,
		logger:    logger,
		handler:   handler,
		cfg:       cfg,
		providers: providers,
		apiKey:    plaintext,
		keyID:     keyID,
	}
	t.Cleanup(func() { h.closeLogger() })
	return h
}

func (h *responsesTestHarness) buildAuthenticatedRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	pk, err := h.store.GetActiveProxyKeyByPrefix(context.Background(), h.apiKey[:12])
	require.NoError(t, err)
	ctx := proxy.ContextWithProxyKey(req.Context(), pk)
	return req.WithContext(ctx)
}

// --- Responses API passthrough response helpers ---

func responsesSuccessJSON() string {
	return `{
		"id": "resp_test123",
		"object": "response",
		"created_at": 1741000000,
		"model": "gpt-4o",
		"status": "completed",
		"output": [{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Hello"}]}],
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"total_tokens": 150,
			"input_tokens_details": {"cached_tokens": 30}
		}
	}`
}

func responsesSSEStream() string {
	var b strings.Builder
	b.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream1\",\"model\":\"gpt-4o\"}}\n\n")
	b.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n")
	b.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":50,\"input_tokens_details\":{\"cached_tokens\":30}}}}\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func TestResponsesProxy_NonStreaming_Passthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)

		// Verify the model was passed through.
		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", reqBody["model"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesSuccessJSON()))
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"Hello, world!"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "resp_test123", resp["id"])
	require.Equal(t, "response", resp["object"])
	require.Equal(t, "completed", resp["status"])

	usage, ok := resp["usage"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(100), usage["input_tokens"])
	require.Equal(t, float64(50), usage["output_tokens"])

	// Wait for the async logger to flush.
	h.closeLogger()

	// Verify a log entry was created.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.Equal(t, "gpt-4o", logs[0].ModelRequested)
	require.Equal(t, "gpt-4o", logs[0].ModelUpstream)
	require.Equal(t, "responses", logs[0].SourceFormat)
	require.Equal(t, http.StatusOK, logs[0].Status)
	require.Equal(t, int64(70), logs[0].InputTokens)
	require.Equal(t, int64(50), logs[0].OutputTokens)
	require.Equal(t, int64(30), logs[0].CacheReadInputTokens)
	require.False(t, logs[0].IsStreaming)
}

func TestResponsesProxy_Streaming_Passthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesSSEStream()))
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"Stream test","stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")

	body := rec.Body.String()
	require.Contains(t, body, "response.created")
	require.Contains(t, body, "response.completed")
	require.Contains(t, body, "[DONE]")

	// Wait for the async logger to flush.
	h.closeLogger()

	// Verify a log entry was created for the streaming request.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.True(t, logs[0].IsStreaming)
	require.Equal(t, "responses", logs[0].SourceFormat)
	require.Equal(t, http.StatusOK, logs[0].Status)
	require.Equal(t, int64(70), logs[0].InputTokens)
	require.Equal(t, int64(50), logs[0].OutputTokens)
	require.Equal(t, int64(30), logs[0].CacheReadInputTokens)
}

func TestResponsesProxy_MethodNotAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for wrong methods")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodGet, "/v1/responses", "")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestResponsesProxy_InvalidJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for invalid JSON")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", "not-json{{{")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResponsesProxy_MissingModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called when model is missing")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"input":"Hello, world!"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	require.NoError(t, err)

	errObj, ok := errResp["error"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, errObj["message"], "Missing required field: model")
}

func TestResponsesProxy_UnknownModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for unknown models")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"nonexistent-model","input":"Hello"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	require.NoError(t, err)

	errObj, ok := errResp["error"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, errObj["message"], "Unknown model")
}

func TestResponsesProxy_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"Internal server error"}}`))
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"Hello"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	// Retryable upstream failures now exhaust to a generic 503, matching the
	// fallback contract used by the other proxy handlers.
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	// Wait for the async logger.
	h.closeLogger()

	// Verify error was logged.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, http.StatusServiceUnavailable, logs[0].Status)
}

func TestResponsesProxy_ErrorLogged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"Something went wrong"}}`))
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"Hello"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	// Wait for the async logger to flush.
	h.closeLogger()

	// Verify error details were logged.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.NotNil(t, logs[0].ErrorDetails)
	require.Contains(t, *logs[0].ErrorDetails, "all 1 fallback entries exhausted; last status 500")
	require.Equal(t, "responses", logs[0].SourceFormat)
	require.Equal(t, "gpt-4o", logs[0].ModelRequested)
	require.Equal(t, "gpt-4o", logs[0].ModelUpstream)
}

func TestResponsesProxy_InvalidInputFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for invalid input")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	// Input is a number (not string or array).
	reqBody := `{"model":"gpt-4o","input":42}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Input must be a string or array")
}

func TestResponsesProxy_TemperatureOutOfRange(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for invalid temperature")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"hi","temperature":3.0}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Temperature must be between 0 and 2")
}

func TestResponsesProxy_DuplicateToolNames(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for duplicate tool names")
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4o","input":"hi","tools":[{"type":"function","name":"my_tool"},{"type":"function","name":"my_tool"}]}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Duplicate tool name")
}

func newResponsesFallbackHarness(t *testing.T, primaryURL, secondaryURL string) *responsesTestHarness {
	t.Helper()

	st := cloneTestDB(t)
	plaintext := templateKey.Plaintext
	keyID := templateKey.ID

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test-master-key"
providers:
  - name: primary
    type: openai_responses
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
  - name: secondary
    type: openai_responses
    base_url: %q
    auth_mode: proxy_key
    api_key: key2
model_list:
  - model_name: virtual-responses
    fallbacks: [primary-model, secondary-model]
  - model_name: primary-model
    provider: primary
    upstream_model: gpt-4o
  - model_name: secondary-model
    provider: secondary
    upstream_model: gpt-4o-mini
`, primaryURL, secondaryURL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	primaryClient, err := openaiProv.NewClient("primary", primaryURL, "proxy_key", "key1", openaiProv.APITypeResponses)
	require.NoError(t, err)
	secondaryClient, err := openaiProv.NewClient("secondary", secondaryURL, "proxy_key", "key2", openaiProv.APITypeResponses)
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"primary":   primaryClient,
		"secondary": secondaryClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewResponsesHandler(cfg, providers, calc, logger, nil, nil, nil)

	h := &responsesTestHarness{
		store:     st,
		logger:    logger,
		handler:   handler,
		cfg:       cfg,
		providers: providers,
		apiKey:    plaintext,
		keyID:     keyID,
	}
	t.Cleanup(func() { h.closeLogger() })
	return h
}

func TestResponsesProxy_Fallback_5xxTriggersRetry(t *testing.T) {
	var primaryCalls, secondaryCalls int

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"primary failed"}}`))
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++

		var reqBody map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		require.Equal(t, "gpt-4o-mini", reqBody["model"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesSuccessJSON()))
	}))
	defer secondary.Close()

	h := newResponsesFallbackHarness(t, primary.URL, secondary.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", `{"model":"virtual-responses","input":"hello"}`)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, primaryCalls)
	require.Equal(t, 1, secondaryCalls)

	h.closeLogger()

	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
	require.Equal(t, "gpt-4o-mini", logs[0].ModelUpstream)
}

package proxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
)

// testHarness bundles all the resources needed for a proxy handler test.
type testHarness struct {
	store     *store.SQLiteStore
	logger    *proxy.AsyncLogger
	handler   *proxy.Handler
	cfg       *config.Config
	providers map[string]provider.Provider
	apiKey    string // plaintext proxy key for auth
	keyID     string
	closeOnce sync.Once
}

// closeLogger drains the async logger exactly once, safe to call multiple times.
func (h *testHarness) closeLogger() {
	h.closeOnce.Do(func() {
		h.logger.Close()
	})
}

// newTestHarness creates a fully wired test harness with a mock upstream server.
// The caller must call cleanup() when done.
func newTestHarness(t *testing.T, upstreamURL string) *testHarness {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.Migrate(context.Background())
	require.NoError(t, err)

	// Generate and store a proxy key.
	plaintext, hash, prefix, err := auth.GenerateKey()
	require.NoError(t, err)

	keyID := "test-key-id"
	err = st.CreateProxyKey(context.Background(), keyID, hash, prefix, "test-key")
	require.NoError(t, err)

	cfg := &config.Config{
		MasterKey: "test-master-key",
		Listen:    ":4000",
		Providers: []config.ProviderConfig{
			{
				Name:           "anthropic",
				BaseURL:        upstreamURL,
				AuthMode:       "proxy_key",
				APIKey:         "test-upstream-key",
				DefaultVersion: "2023-06-01",
			},
		},
		ModelList: []config.ModelMapping{
			{
				ModelName:     "claude-sonnet",
				Provider:      "anthropic",
				UpstreamModel: "claude-sonnet-4-20250514",
			},
			{
				ModelName:     "claude-opus",
				Provider:      "anthropic",
				UpstreamModel: "claude-opus-4-20250514",
			},
		},
	}

	anthropicClient, err := anthropic.NewClient("anthropic", upstreamURL, "proxy_key", "test-upstream-key", "2023-06-01")
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"anthropic": anthropicClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)

	h := &testHarness{
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

// buildAuthenticatedRequest creates an HTTP request with the proxy key set in context.
func (h *testHarness) buildAuthenticatedRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Look up the proxy key and inject it into context the same way the middleware would.
	pk, err := h.store.GetActiveProxyKeyByPrefix(context.Background(), h.apiKey[:12])
	require.NoError(t, err)
	ctx := proxy.ContextWithProxyKey(req.Context(), pk)
	return req.WithContext(ctx)
}

func TestAnthropicProxy_NonStreaming(t *testing.T) {
	// Set up a mock upstream server returning a canned Anthropic response.
	cannedResp := anthropic.MessagesResponse{
		ID:   "msg_test123",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Hello from the mock!"},
		},
		Model:      "claude-sonnet-4-20250514",
		StopReason: strPtr("end_turn"),
		Usage: anthropic.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)

		// Verify upstream received the correct model.
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqBody map[string]interface{}
		err = json.Unmarshal(body, &reqBody)
		require.NoError(t, err)
		require.Equal(t, "claude-sonnet-4-20250514", reqBody["model"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp anthropic.MessagesResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "msg_test123", resp.ID)
	require.Equal(t, "assistant", resp.Role)
	require.Len(t, resp.Content, 1)
	require.Equal(t, "Hello from the mock!", resp.Content[0].Text)
	require.Equal(t, int64(100), resp.Usage.InputTokens)
	require.Equal(t, int64(50), resp.Usage.OutputTokens)

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
	require.Equal(t, "claude-sonnet", logs[0].ModelRequested)
	require.Equal(t, "claude-sonnet-4-20250514", logs[0].ModelUpstream)
	require.Equal(t, "anthropic", logs[0].SourceFormat)
	require.Equal(t, http.StatusOK, logs[0].Status)
	require.Equal(t, int64(100), logs[0].InputTokens)
	require.Equal(t, int64(50), logs[0].OutputTokens)
}

func TestAnthropicProxy_Streaming(t *testing.T) {
	// Build a mock SSE stream as the upstream would return.
	ssePayload := buildAnthropicSSEStream("msg_stream1", 200, 75, "Hello streaming!")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Stream test"}],"max_tokens":100,"stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")

	body := rec.Body.String()
	require.Contains(t, body, "message_start")
	require.Contains(t, body, "content_block_delta")
	require.Contains(t, body, "message_stop")

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
	require.Equal(t, http.StatusOK, logs[0].Status)
}

func TestAnthropicProxy_AuthRequired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called when auth fails")
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	// Send a request without injecting a proxy key into context.
	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Use the AuthMiddleware to protect the handler.
	middlewareChain := proxy.AuthMiddleware(h.store)(h.handler)

	rec := httptest.NewRecorder()
	middlewareChain.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)

	var errResp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	require.NoError(t, err)

	errObj, ok := errResp["error"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "authentication_error", errObj["type"])
}

func TestAnthropicProxy_UnknownModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for unknown models")
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"nonexistent-model","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

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

func TestAnthropicProxy_MethodNotAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for wrong methods")
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodGet, "/v1/messages", "")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestAnthropicProxy_InvalidJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for invalid JSON")
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", "not-json{{{")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAnthropicProxy_MissingModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called when model is missing")
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

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

func TestAnthropicProxy_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "error",
			"error": map[string]string{
				"type":    "rate_limit_error",
				"message": "Rate limit exceeded",
			},
		}))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	// A 429 from the only chain entry exhausts the chain → 503.
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
	require.NotNil(t, logs[0].ErrorDetails)
}

// --- helpers ---

func strPtr(s string) *string { return &s }

// buildFallbackRequest creates an authenticated request for fallback tests.
// Since these use direct Config construction (no Load), we inject the key via context.
func buildFallbackRequest(t *testing.T, st *store.SQLiteStore, _, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Find the key we just created via prefix scan.
	keys, err := st.ListActiveProxyKeys(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	pk, err := st.GetActiveProxyKeyByPrefix(context.Background(), keys[0].KeyPrefix)
	require.NoError(t, err)
	ctx := proxy.ContextWithProxyKey(req.Context(), pk)
	return req.WithContext(ctx)
}

// TestFallback_5xxTriggersRetry verifies that a 5xx from the primary triggers fallback to secondary.
func TestFallback_5xxTriggersRetry(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	cannedResp := anthropicSuccessResponse()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer secondary.Close()

	// Build a config with a virtual model referencing primary then secondary.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: primary
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
    default_version: "2023-06-01"
  - name: secondary
    base_url: %q
    auth_mode: proxy_key
    api_key: key2
    default_version: "2023-06-01"
model_list:
  - model_name: virtual
    fallbacks: [primary-model, secondary-model]
  - model_name: primary-model
    provider: primary
    upstream_model: claude-3
  - model_name: secondary-model
    provider: secondary
    upstream_model: claude-3
`, primary.URL, secondary.URL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	st, _ := setupFallbackStore(t)
	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	// No defer — we close explicitly below to flush before reading logs.

	primaryClient, err := anthropic.NewClient("primary", primary.URL, "proxy_key", "key1", "2023-06-01")
	require.NoError(t, err)
	secondaryClient, err := anthropic.NewClient("secondary", secondary.URL, "proxy_key", "key2", "2023-06-01")
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"primary":   primaryClient,
		"secondary": secondaryClient,
	}

	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)
	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, primaryHits, "primary should be attempted once")

	// Verify FallbackAttempts = 2 in the log.
	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

// TestFallback_429TriggersRetry verifies that 429 triggers fallback.
func TestFallback_429TriggersRetry(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer primary.Close()

	cannedResp := anthropicSuccessResponse()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer secondary.Close()

	handler, st, logger := buildVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestFallback_4xxNoRetry verifies that non-429 4xx is returned immediately.
func TestFallback_4xxNoRetry(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer primary.Close()

	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer secondary.Close()

	handler, st, logger := buildVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 400 is not a fallback status — client gets it immediately without retrying.
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1, primaryHits, "primary should be attempted once")
	require.Equal(t, 0, secondaryHits, "secondary should never be reached")
}

// TestFallback_AllExhausted503 verifies that exhausting all entries returns 503.
func TestFallback_AllExhausted503(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer secondary.Close()

	handler, st, logger := buildVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestFallback_FirstSuccessShortCircuits verifies no unnecessary fallbacks when first entry succeeds.
func TestFallback_FirstSuccessShortCircuits(t *testing.T) {
	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer secondary.Close()

	cannedResp := anthropicSuccessResponse()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer primary.Close()

	handler, st, logger := buildVirtualFallbackHandler(t, primary.URL, secondary.URL)

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, secondaryHits, "secondary should not be hit on first-entry success")

	// FallbackAttempts should be 1 (no fallback needed).
	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(1), logs[0].FallbackAttempts)
}

// TestFallback_FallbackAttempts_Count verifies attempt counts in log entries (T020).
func TestFallback_FallbackAttempts_Count(t *testing.T) {
	// Build primary (fails) + secondary (succeeds).
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	cannedResp := anthropicSuccessResponse()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer secondary.Close()

	handler, st, logger := buildVirtualFallbackHandler(t, primary.URL, secondary.URL)

	// Request 1: first-entry success (direct model, not virtual).
	cannedPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer cannedPrimary.Close()

	// Use virtual model — primary fails, secondary succeeds → attempts = 2.
	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts, "one fallback means attempt count = 2")
}

// --- fallback test utilities ---

// anthropicSuccessResponse returns a minimal valid Anthropic response.
func anthropicSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"content":     []map[string]interface{}{{"type": "text", "text": "ok"}},
		"model":       "claude-3",
		"stop_reason": "end_turn",
		"usage":       map[string]interface{}{"input_tokens": 5, "output_tokens": 1},
	}
}

// setupFallbackStore creates a minimal store for fallback tests.
func setupFallbackStore(t *testing.T) (*store.SQLiteStore, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fbt.db")
	st, err := store.NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))

	_, hash, prefix, err := auth.GenerateKey()
	require.NoError(t, err)
	require.NoError(t, st.CreateProxyKey(context.Background(), "fbt-id", hash, prefix, "fbt"))
	return st, prefix
}

// buildVirtualFallbackHandler builds a handler with a two-entry virtual model "virtual".
func buildVirtualFallbackHandler(t *testing.T, primaryURL, secondaryURL string) (*proxy.Handler, *store.SQLiteStore, *proxy.AsyncLogger) {
	t.Helper()
	st, _ := setupFallbackStore(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: primary
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
    default_version: "2023-06-01"
  - name: secondary
    base_url: %q
    auth_mode: proxy_key
    api_key: key2
    default_version: "2023-06-01"
model_list:
  - model_name: virtual
    fallbacks: [primary-model, secondary-model]
  - model_name: primary-model
    provider: primary
    upstream_model: claude-3
  - model_name: secondary-model
    provider: secondary
    upstream_model: claude-3
`, primaryURL, secondaryURL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	primaryClient, err := anthropic.NewClient("primary", primaryURL, "proxy_key", "key1", "2023-06-01")
	require.NoError(t, err)
	secondaryClient, err := anthropic.NewClient("secondary", secondaryURL, "proxy_key", "key2", "2023-06-01")
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"primary":   primaryClient,
		"secondary": secondaryClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)
	return handler, st, logger
}

// --- Cross-format fallback tests (T039) ---

// openAISuccessResponse returns a minimal valid Chat Completions response.
func openAISuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1741000000,
		"model":   "gpt-4o",
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       map[string]interface{}{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
	}
}

// responsesSuccessResponse returns a minimal valid Responses API response.
func responsesSuccessResponse() map[string]interface{} {
	return map[string]interface{}{
		"id":         "resp_test",
		"object":     "response",
		"created_at": 1741000000,
		"model":      "gpt-4o",
		"status":     "completed",
		"output": []map[string]interface{}{
			{
				"type": "message",
				"role": "assistant",
				"content": []map[string]interface{}{
					{"type": "output_text", "text": "ok"},
				},
			},
		},
		"usage": map[string]interface{}{"input_tokens": 5, "output_tokens": 1, "total_tokens": 6},
	}
}

// buildCrossFormatFallbackHandler creates an Anthropic handler with a virtual model
// where primary is Anthropic and secondary is a different format (OpenAI CC or Responses).
func buildCrossFormatFallbackHandler(t *testing.T, primaryURL, secondaryURL, secondaryType string) (*proxy.Handler, *store.SQLiteStore, *proxy.AsyncLogger) {
	t.Helper()
	st, _ := setupFallbackStore(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var secondaryYAML string
	if secondaryType == "openai" || secondaryType == "openai_responses" {
		secondaryYAML = fmt.Sprintf(`
  - name: secondary
    type: %s
    base_url: %q
    auth_mode: proxy_key
    api_key: key2`, secondaryType, secondaryURL)
	}

	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: primary
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
    default_version: "2023-06-01"
%s
model_list:
  - model_name: virtual
    fallbacks: [primary-model, secondary-model]
  - model_name: primary-model
    provider: primary
    upstream_model: claude-3
  - model_name: secondary-model
    provider: secondary
    upstream_model: gpt-4o
`, primaryURL, secondaryYAML)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	var apiType string
	switch secondaryType {
	case "openai_responses":
		apiType = openai.APITypeResponses
	default:
		apiType = openai.APITypeChatCompletions
	}

	providers := map[string]provider.Provider{}

	primaryClient, err2 := anthropic.NewClient("primary", primaryURL, "proxy_key", "key1", "2023-06-01")
	require.NoError(t, err2)
	providers["primary"] = primaryClient

	secondaryClient, err2 := openai.NewClient("secondary", secondaryURL, "proxy_key", "key2", apiType)
	require.NoError(t, err2)
	providers["secondary"] = secondaryClient

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)
	return handler, st, logger
}

// TestFallback_AnthropicToOpenAICC verifies that a failing Anthropic primary falls back
// to an OpenAI Chat Completions secondary with cross-format translation.
func TestFallback_AnthropicToOpenAICC(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request was sent to the CC endpoint.
		require.Equal(t, "/v1/chat/completions", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(openAISuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildCrossFormatFallbackHandler(t, primary.URL, secondary.URL, "openai")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify response is in Anthropic format (translated from CC).
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "assistant", resp["role"])

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts, "should be attempt 2 after primary failed")
}

// TestFallback_AnthropicToResponsesAPI verifies that a failing Anthropic primary falls back
// to a Responses API secondary with cross-format translation.
func TestFallback_AnthropicToResponsesAPI(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request was sent to the Responses endpoint.
		require.Equal(t, "/v1/responses", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(responsesSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildCrossFormatFallbackHandler(t, primary.URL, secondary.URL, "openai_responses")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify response is in Anthropic format (translated from Responses).
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "assistant", resp["role"])

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

// TestFallback_AnthropicPrimary_OpenAICC_BothFail verifies both Anthropic and OpenAI CC
// entries exhausted returns 502 (the OpenAI dispatch handles end-to-end without further fallback).
func TestFallback_AnthropicPrimary_OpenAICC_BothFail(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer secondary.Close()

	handler, st, logger := buildCrossFormatFallbackHandler(t, primary.URL, secondary.URL, "openai")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Both fallback entries fail, so the handler returns an upstream failure.
	require.True(t, rec.Code >= 400, "should return error status when both fail")

	logger.Close()
}

func buildAnthropicCrossFormatMiddleFallbackHandler(t *testing.T, primaryURL, secondaryURL, tertiaryURL, secondaryType string) (*proxy.Handler, *store.SQLiteStore, *proxy.AsyncLogger) {
	t.Helper()
	st, _ := setupFallbackStore(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var secondaryYAML string
	if secondaryType == "openai" || secondaryType == "openai_responses" {
		secondaryYAML = fmt.Sprintf(`
  - name: secondary
    type: %s
    base_url: %q
    auth_mode: proxy_key
    api_key: key2`, secondaryType, secondaryURL)
	}

	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: primary
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
    default_version: "2023-06-01"
%s
  - name: tertiary
    base_url: %q
    auth_mode: proxy_key
    api_key: key3
    default_version: "2023-06-01"
model_list:
  - model_name: virtual
    fallbacks: [primary-model, secondary-model, tertiary-model]
  - model_name: primary-model
    provider: primary
    upstream_model: claude-3
  - model_name: secondary-model
    provider: secondary
    upstream_model: gpt-4o
  - model_name: tertiary-model
    provider: tertiary
    upstream_model: claude-3-5
`, primaryURL, secondaryYAML, tertiaryURL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	var apiType string
	switch secondaryType {
	case "openai_responses":
		apiType = openai.APITypeResponses
	default:
		apiType = openai.APITypeChatCompletions
	}

	providers := map[string]provider.Provider{}

	primaryClient, err2 := anthropic.NewClient("primary", primaryURL, "proxy_key", "key1", "2023-06-01")
	require.NoError(t, err2)
	providers["primary"] = primaryClient

	secondaryClient, err2 := openai.NewClient("secondary", secondaryURL, "proxy_key", "key2", apiType)
	require.NoError(t, err2)
	providers["secondary"] = secondaryClient

	tertiaryClient, err2 := anthropic.NewClient("tertiary", tertiaryURL, "proxy_key", "key3", "2023-06-01")
	require.NoError(t, err2)
	providers["tertiary"] = tertiaryClient

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)
	return handler, st, logger
}

func TestFallback_Anthropic_OpenAISecondaryRetriesToTertiary(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryHits++
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer secondary.Close()

	tertiaryHits := 0
	tertiary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tertiaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer tertiary.Close()

	handler, st, logger := buildAnthropicCrossFormatMiddleFallbackHandler(t, primary.URL, secondary.URL, tertiary.URL, "openai")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, secondaryHits)
	require.Equal(t, 1, tertiaryHits)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(3), logs[0].FallbackAttempts)
}

func TestFallback_Anthropic_ResponsesSecondaryRetriesToTertiary(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryHits++
		require.Equal(t, "/v1/responses", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer secondary.Close()

	tertiaryHits := 0
	tertiary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tertiaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer tertiary.Close()

	handler, st, logger := buildAnthropicCrossFormatMiddleFallbackHandler(t, primary.URL, secondary.URL, tertiary.URL, "openai_responses")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, secondaryHits)
	require.Equal(t, 1, tertiaryHits)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(3), logs[0].FallbackAttempts)
}

// buildAnthropicSSEStream constructs a valid Anthropic SSE stream payload.
func buildAnthropicSSEStream(msgID string, inputTokens, outputTokens int64, text string) string {
	var b strings.Builder

	// message_start
	msgStart := fmt.Sprintf(`{"type":"message_start","message":{"id":"%s","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":%d,"output_tokens":0}}}`, msgID, inputTokens)
	b.WriteString("event: message_start\n")
	b.WriteString("data: " + msgStart + "\n\n")

	// content_block_start
	b.WriteString("event: content_block_start\n")
	b.WriteString(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n")

	// content_block_delta
	deltaData := fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%s"}}`, text)
	b.WriteString("event: content_block_delta\n")
	b.WriteString("data: " + deltaData + "\n\n")

	// content_block_stop
	b.WriteString("event: content_block_stop\n")
	b.WriteString(`data: {"type":"content_block_stop","index":0}` + "\n\n")

	// message_delta
	msgDelta := fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, outputTokens)
	b.WriteString("event: message_delta\n")
	b.WriteString("data: " + msgDelta + "\n\n")

	// message_stop
	b.WriteString("event: message_stop\n")
	b.WriteString(`data: {"type":"message_stop"}` + "\n\n")

	return b.String()
}

// buildAnthropicThinkingSSEStream constructs an Anthropic SSE stream with a thinking block
// (index 0) followed by a text block (index 1), as returned by models with extended thinking.
func buildAnthropicThinkingSSEStream(msgID string, inputTokens, outputTokens int64, thinkingText, text string) string {
	var b strings.Builder

	msgStart := fmt.Sprintf(`{"type":"message_start","message":{"id":"%s","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","stop_reason":null,"usage":{"input_tokens":%d,"output_tokens":0}}}`, msgID, inputTokens)
	b.WriteString("event: message_start\n")
	b.WriteString("data: " + msgStart + "\n\n")

	// thinking block
	b.WriteString("event: content_block_start\n")
	b.WriteString(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n\n")
	b.WriteString("event: content_block_delta\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"%s"}}`, thinkingText) + "\n\n")
	b.WriteString("event: content_block_delta\n")
	b.WriteString(`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc123"}}` + "\n\n")
	b.WriteString("event: content_block_stop\n")
	b.WriteString(`data: {"type":"content_block_stop","index":0}` + "\n\n")

	// text block
	b.WriteString("event: content_block_start\n")
	b.WriteString(`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n")
	b.WriteString("event: content_block_delta\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"%s"}}`, text) + "\n\n")
	b.WriteString("event: content_block_stop\n")
	b.WriteString(`data: {"type":"content_block_stop","index":1}` + "\n\n")

	msgDelta := fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, outputTokens)
	b.WriteString("event: message_delta\n")
	b.WriteString("data: " + msgDelta + "\n\n")

	b.WriteString("event: message_stop\n")
	b.WriteString(`data: {"type":"message_stop"}` + "\n\n")

	return b.String()
}

// buildResponsesSSEStream constructs a minimal Responses API SSE stream.
func buildResponsesSSEStream(respID, model, text string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`data: {"type":"response.created","response":{"id":"%s","object":"response","model":"%s","status":"in_progress","output":[]}}`, respID, model) + "\n\n")
	b.WriteString(`data: {"type":"output_item.added","output_index":0,"item":{"id":"item_1","type":"message","role":"assistant","content":[]}}` + "\n\n")
	b.WriteString(`data: {"type":"content_part.added","item_id":"item_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}` + "\n\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"%s"}`, text) + "\n\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"content_part.done","item_id":"item_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"%s"}}`, text) + "\n\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"output_item.done","output_index":0,"item":{"id":"item_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"%s"}]}}`, text) + "\n\n")
	b.WriteString(fmt.Sprintf(`data: {"type":"response.completed","response":{"id":"%s","object":"response","model":"%s","status":"completed","output":[{"id":"item_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"%s"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`, respID, model, text) + "\n\n")
	b.WriteString("data: [DONE]\n\n")

	return b.String()
}

// --- Thinking header forwarding tests ---

// TestAnthropicProxy_Streaming_ForwardsAnthropicBetaHeader verifies that the
// anthropic-beta response header echoed by the upstream is forwarded to the client.
// Claude Code validates this header before rendering thinking blocks.
func TestAnthropicProxy_Streaming_ForwardsAnthropicBetaHeader(t *testing.T) {
	ssePayload := buildAnthropicSSEStream("msg_think1", 100, 50, "Hello with thinking!")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Think"}],"max_tokens":100,"stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "interleaved-thinking-2025-05-14", rec.Header().Get("Anthropic-Beta"),
		"anthropic-beta must be forwarded to the client for thinking block rendering")
}

// TestAnthropicProxy_Streaming_ForwardsMultipleAnthropicHeaders verifies that all
// anthropic-* and x-request-id response headers from the upstream reach the client.
func TestAnthropicProxy_Streaming_ForwardsMultipleAnthropicHeaders(t *testing.T) {
	ssePayload := buildAnthropicSSEStream("msg_think2", 100, 50, "Multi-header test")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "999")
		w.Header().Set("X-Request-Id", "req-abc123")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Headers"}],"max_tokens":100,"stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "interleaved-thinking-2025-05-14", rec.Header().Get("Anthropic-Beta"))
	require.Equal(t, "999", rec.Header().Get("Anthropic-Ratelimit-Requests-Remaining"))
	require.Equal(t, "req-abc123", rec.Header().Get("X-Request-Id"))
}

// TestAnthropicProxy_Streaming_NonAnthropicHeadersNotForwarded verifies that arbitrary
// upstream headers are not forwarded — only anthropic-* and x-request-id.
func TestAnthropicProxy_Streaming_NonAnthropicHeadersNotForwarded(t *testing.T) {
	ssePayload := buildAnthropicSSEStream("msg_think3", 100, 50, "Header filter test")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		w.Header().Set("X-Custom-Upstream-Header", "should-not-appear")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Filter test"}],"max_tokens":100,"stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "interleaved-thinking-2025-05-14", rec.Header().Get("Anthropic-Beta"))
	require.Empty(t, rec.Header().Get("X-Custom-Upstream-Header"), "arbitrary upstream headers must not be forwarded")
}

// TestAnthropicProxy_Streaming_ThinkingEventsPassThrough verifies that thinking
// content block events in Anthropic SSE streams are relayed verbatim to the client.
func TestAnthropicProxy_Streaming_ThinkingEventsPassThrough(t *testing.T) {
	ssePayload := buildAnthropicThinkingSSEStream("msg_think4", 200, 100, "My reasoning", "My answer")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"Think deeply"}],"max_tokens":500,"stream":true,"thinking":{"type":"enabled","budget_tokens":1024}}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "interleaved-thinking-2025-05-14", rec.Header().Get("Anthropic-Beta"))

	body := rec.Body.String()
	require.Contains(t, body, "thinking", "thinking content block type must appear in stream")
	require.Contains(t, body, "My reasoning", "thinking text must pass through verbatim")
	require.Contains(t, body, "My answer", "text content must also pass through")
}

// TestResponsesProvider_Streaming_InjectsAnthropicBetaHeader verifies that when the
// upstream is a Responses API provider, the proxy injects Anthropic-Beta even though
// the upstream never sends that header.
func TestResponsesProvider_Streaming_InjectsAnthropicBetaHeader(t *testing.T) {
	ssePayload := buildResponsesSSEStream("resp_test", "gpt-4o", "ok")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		// Deliberately do NOT set Anthropic-Beta — proxy must inject it.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	st, _ := setupFallbackStore(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: chatgpt
    type: openai_responses
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
model_list:
  - model_name: chatgpt-model
    provider: chatgpt
    upstream_model: gpt-4o
`, upstream.URL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	chatgptClient, err := openai.NewClient("chatgpt", upstream.URL, "proxy_key", "key1", openai.APITypeResponses)
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"chatgpt": chatgptClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	defer logger.Close()
	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)

	reqBody := `{"model":"chatgpt-model","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"stream":true}`
	req := buildFallbackRequest(t, st, "", reqBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "interleaved-thinking-2025-05-14", rec.Header().Get("Anthropic-Beta"),
		"proxy must inject Anthropic-Beta for the Responses API path even without upstream sending it")
}

func TestResponsesProvider_NonStreaming_LogsCacheReadTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         "resp_cache",
			"object":     "response",
			"created_at": 1741000000,
			"model":      "gpt-4o",
			"status":     "completed",
			"output": []map[string]interface{}{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]interface{}{
						{"type": "output_text", "text": "cached"},
					},
				},
			},
			"usage": map[string]interface{}{
				"input_tokens":  50,
				"output_tokens": 10,
				"total_tokens":  60,
				"input_tokens_details": map[string]interface{}{
					"cached_tokens": 30,
				},
			},
		}))
	}))
	defer upstream.Close()

	st, _ := setupFallbackStore(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
  - name: chatgpt
    type: openai_responses
    base_url: %q
    auth_mode: proxy_key
    api_key: key1
model_list:
  - model_name: chatgpt-model
    provider: chatgpt
    upstream_model: gpt-4o
`, upstream.URL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	chatgptClient, err := openai.NewClient("chatgpt", upstream.URL, "proxy_key", "key1", openai.APITypeResponses)
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"chatgpt": chatgptClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewHandler(cfg, providers, calc, logger, nil)

	reqBody := `{"model":"chatgpt-model","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`
	req := buildFallbackRequest(t, st, "", reqBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(30), logs[0].CacheReadInputTokens)
}

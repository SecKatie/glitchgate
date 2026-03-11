package proxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
	"codeberg.org/kglitchy/llm-proxy/internal/config"
	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/provider"
	"codeberg.org/kglitchy/llm-proxy/internal/provider/anthropic"
	"codeberg.org/kglitchy/llm-proxy/internal/proxy"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
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

	providers := map[string]provider.Provider{
		"anthropic": anthropic.NewClient("anthropic", upstreamURL, "proxy_key", "test-upstream-key", "2023-06-01"),
	}

	calc := pricing.NewCalculator(pricing.DefaultPricing)
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewHandler(cfg, providers, calc, logger)

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

	require.Equal(t, http.StatusTooManyRequests, rec.Code)

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

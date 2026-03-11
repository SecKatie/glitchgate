package proxy_test

import (
	"context"
	"encoding/json"
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
	"codeberg.org/kglitchy/llm-proxy/internal/translate"
)

// openAITestHarness bundles resources for OpenAI handler tests.
type openAITestHarness struct {
	store     *store.SQLiteStore
	logger    *proxy.AsyncLogger
	handler   *proxy.OpenAIHandler
	cfg       *config.Config
	providers map[string]provider.Provider
	apiKey    string
	keyID     string
	closeOnce sync.Once
}

// closeLogger drains the async logger exactly once, safe to call multiple times.
func (h *openAITestHarness) closeLogger() {
	h.closeOnce.Do(func() {
		h.logger.Close()
	})
}

func newOpenAITestHarness(t *testing.T, upstreamURL string) *openAITestHarness {
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

	keyID := "test-openai-key-id"
	err = st.CreateProxyKey(context.Background(), keyID, hash, prefix, "test-openai-key")
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
				ModelName:     "gpt-4",
				Provider:      "anthropic",
				UpstreamModel: "claude-sonnet-4-20250514",
			},
		},
	}

	providers := map[string]provider.Provider{
		"anthropic": anthropic.NewClient("anthropic", upstreamURL, "proxy_key", "test-upstream-key", "2023-06-01"),
	}

	calc := pricing.NewCalculator(pricing.DefaultPricing)
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewOpenAIHandler(cfg, providers, calc, logger)

	h := &openAITestHarness{
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

func (h *openAITestHarness) buildAuthenticatedRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	pk, err := h.store.GetActiveProxyKeyByPrefix(context.Background(), h.apiKey[:12])
	require.NoError(t, err)
	ctx := proxy.ContextWithProxyKey(req.Context(), pk)
	return req.WithContext(ctx)
}

func TestOpenAIProxy_NonStreaming(t *testing.T) {
	// Mock upstream returns a canned Anthropic response.
	cannedResp := anthropic.MessagesResponse{
		ID:   "msg_oai_test1",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Translated response"},
		},
		Model:      "claude-sonnet-4-20250514",
		StopReason: strPtr("end_turn"),
		Usage: anthropic.Usage{
			InputTokens:  150,
			OutputTokens: 80,
		},
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)

		// Verify the request was translated to Anthropic format.
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqBody map[string]interface{}
		err = json.Unmarshal(body, &reqBody)
		require.NoError(t, err)

		// Should have been translated to the upstream model name.
		require.Equal(t, "claude-sonnet-4-20250514", reqBody["model"])
		// Should have max_tokens set.
		require.NotNil(t, reqBody["max_tokens"])
		// Should have messages in Anthropic format (no system role in messages).
		messages, ok := reqBody["messages"].([]interface{})
		require.True(t, ok)
		require.Greater(t, len(messages), 0)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4","messages":[{"role":"user","content":"Hello from OpenAI format"}],"max_tokens":200}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/chat/completions", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp translate.ChatCompletionResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "chat.completion", resp.Object)
	require.Equal(t, "chatcmpl-msg_oai_test1", resp.ID)
	require.Equal(t, "gpt-4", resp.Model)
	require.Len(t, resp.Choices, 1)
	require.NotNil(t, resp.Choices[0].Message)
	require.Equal(t, "assistant", resp.Choices[0].Message.Role)
	require.Equal(t, "Translated response", resp.Choices[0].Message.Content)
	require.NotNil(t, resp.Choices[0].FinishReason)
	require.Equal(t, "stop", *resp.Choices[0].FinishReason)

	// Verify usage was translated.
	require.NotNil(t, resp.Usage)
	require.Equal(t, int64(150), resp.Usage.PromptTokens)
	require.Equal(t, int64(80), resp.Usage.CompletionTokens)
	require.Equal(t, int64(230), resp.Usage.TotalTokens)

	// Wait for the async logger to flush.
	h.closeLogger()

	// Verify log entry was created.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	require.Equal(t, "openai", logs[0].SourceFormat)
	require.Equal(t, http.StatusOK, logs[0].Status)
}

func TestOpenAIProxy_Streaming(t *testing.T) {
	// Build a mock Anthropic SSE stream.
	ssePayload := buildAnthropicSSEStream("msg_oai_stream1", 300, 120, "Streaming via OpenAI format")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the stream flag was translated.
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqBody map[string]interface{}
		err = json.Unmarshal(body, &reqBody)
		require.NoError(t, err)
		require.Equal(t, true, reqBody["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)

	reqBody := `{"model":"gpt-4","messages":[{"role":"user","content":"Stream test"}],"max_tokens":100,"stream":true}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/chat/completions", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")

	body := rec.Body.String()
	// The stream translator should produce OpenAI-format SSE data lines.
	require.Contains(t, body, "data: ")
	require.Contains(t, body, "chat.completion.chunk")
	require.Contains(t, body, "[DONE]")

	// Wait for the async logger to flush.
	h.closeLogger()

	// Verify log entry.
	logs, total, err := h.store.ListRequestLogs(context.Background(), store.ListLogsParams{
		Page:    1,
		PerPage: 10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.True(t, logs[0].IsStreaming)
	require.Equal(t, "openai", logs[0].SourceFormat)
}

func TestOpenAIProxy_SystemMessage(t *testing.T) {
	// This test verifies that system messages from OpenAI format are correctly
	// extracted and placed into the Anthropic system field during translation.

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqBody map[string]interface{}
		err = json.Unmarshal(body, &reqBody)
		require.NoError(t, err)

		// The system field should be set from the system message.
		system, ok := reqBody["system"]
		require.True(t, ok, "system field should be present in translated request")
		require.Equal(t, "You are a helpful assistant.", system)

		// Messages should only contain the user message, not the system message.
		messages, ok := reqBody["messages"].([]interface{})
		require.True(t, ok)
		require.Len(t, messages, 1)

		firstMsg, ok := messages[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "user", firstMsg["role"])

		// Return a valid response.
		cannedResp := anthropic.MessagesResponse{
			ID:   "msg_sys_test",
			Type: "message",
			Role: "assistant",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "I am helpful."},
			},
			Model:      "claude-sonnet-4-20250514",
			StopReason: strPtr("end_turn"),
			Usage: anthropic.Usage{
				InputTokens:  50,
				OutputTokens: 30,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(cannedResp))
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)

	reqBody := `{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Hi there"}
		],
		"max_tokens": 100
	}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/chat/completions", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp translate.ChatCompletionResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "I am helpful.", resp.Choices[0].Message.Content)
}

func TestOpenAIProxy_MethodNotAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)

	req := h.buildAuthenticatedRequest(t, http.MethodGet, "/v1/chat/completions", "")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestOpenAIProxy_UnknownModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for unknown models")
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)

	reqBody := `{"model":"unknown-model","messages":[{"role":"user","content":"Hello"}]}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/chat/completions", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp translate.OpenAIErrorResponse
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	require.NoError(t, err)
	require.Contains(t, errResp.Error.Message, "Unknown model")
}

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

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
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

	st := cloneTestDB(t)
	plaintext := templateKey.Plaintext
	keyID := templateKey.ID

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

	anthropicClient, err := anthropic.NewClient("anthropic", upstreamURL, "proxy_key", "test-upstream-key", "2023-06-01")
	require.NoError(t, err)

	providers := map[string]provider.Provider{
		"anthropic": anthropicClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)

	handler := proxy.NewOpenAIHandler(cfg, providers, calc, logger, nil)

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

	var resp openai.ChatCompletionResponse
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

	var resp openai.ChatCompletionResponse
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

	var errResp openai.ErrorResponse
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	require.NoError(t, err)
	require.Contains(t, errResp.Error.Message, "Unknown model")
}

// --- OpenAI fallback tests (T019, T021) ---

// buildOpenAIVirtualFallbackHandler builds an OpenAIHandler with a two-entry virtual model "virtual".
// Requests to "virtual" go to primary first, then secondary.
func buildOpenAIVirtualFallbackHandler(t *testing.T, primaryURL, secondaryURL string) (*proxy.OpenAIHandler, *store.SQLiteStore, *proxy.AsyncLogger) {
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
	handler := proxy.NewOpenAIHandler(cfg, providers, calc, logger, nil)
	return handler, st, logger
}

func TestOpenAIFallback_5xxTriggersRetry(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Return a minimal Anthropic response (OpenAI handler translates Anthropic→OpenAI).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAIVirtualFallbackHandler(t, primary.URL, secondary.URL)

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

func TestOpenAIFallback_429TriggersRetry(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAIVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestOpenAIFallback_4xxNoRetry(t *testing.T) {
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

	handler, st, logger := buildOpenAIVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1, primaryHits)
	require.Equal(t, 0, secondaryHits)
}

func TestOpenAIFallback_AllExhausted503(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAIVirtualFallbackHandler(t, primary.URL, secondary.URL)
	defer logger.Close()

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestOpenAIFallback_FallbackAttempts_Count(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAIVirtualFallbackHandler(t, primary.URL, secondary.URL)

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

func buildOpenAICrossFormatFallbackHandler(t *testing.T, primaryURL, secondaryURL, primaryType string) (*proxy.OpenAIHandler, *store.SQLiteStore, *proxy.AsyncLogger) {
	t.Helper()
	st, _ := setupFallbackStore(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var primaryYAML string
	if primaryType == "openai" || primaryType == "openai_responses" {
		primaryYAML = fmt.Sprintf(`
  - name: primary
    type: %s
    base_url: %q
    auth_mode: proxy_key
    api_key: key1`, primaryType, primaryURL)
	}

	require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
master_key: "test"
providers:
%s
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
    upstream_model: gpt-4o
  - model_name: secondary-model
    provider: secondary
    upstream_model: claude-3
`, primaryYAML, secondaryURL)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	var apiType string
	switch primaryType {
	case "openai_responses":
		apiType = openai.APITypeResponses
	default:
		apiType = openai.APITypeChatCompletions
	}

	providers := map[string]provider.Provider{}

	primaryClient, err2 := openai.NewClient("primary", primaryURL, "proxy_key", "key1", apiType)
	require.NoError(t, err2)
	providers["primary"] = primaryClient

	secondaryClient, err2 := anthropic.NewClient("secondary", secondaryURL, "proxy_key", "key2", "2023-06-01")
	require.NoError(t, err2)
	providers["secondary"] = secondaryClient

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	handler := proxy.NewOpenAIHandler(cfg, providers, calc, logger, nil)
	return handler, st, logger
}

func TestOpenAIFallback_OpenAINativePrimaryRetriesToAnthropic(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAICrossFormatFallbackHandler(t, primary.URL, secondary.URL, "openai")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, primaryHits)
	require.Equal(t, 1, secondaryHits)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

func TestOpenAIFallback_ResponsesPrimaryRetriesToAnthropic(t *testing.T) {
	primaryHits := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
		require.Equal(t, "/v1/responses", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	secondaryHits := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(anthropicSuccessResponse()))
	}))
	defer secondary.Close()

	handler, st, logger := buildOpenAICrossFormatFallbackHandler(t, primary.URL, secondary.URL, "openai_responses")

	req := buildFallbackRequest(t, st, "virtual", `{"model":"virtual","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, primaryHits)
	require.Equal(t, 1, secondaryHits)

	logger.Close()
	logs, total, err := st.ListRequestLogs(context.Background(), store.ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(2), logs[0].FallbackAttempts)
}

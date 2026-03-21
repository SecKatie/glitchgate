// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integration_test exercises the proxy's two public endpoints using the
// official Go SDKs for Anthropic and OpenAI as real clients.  A mock upstream
// Anthropic server handles every request so no real API keys are required.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	openaisdk "github.com/openai/openai-go"
	openaioption "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	llmanthropic "github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
)

// --- Template DB (shared across all tests) ---

var (
	templateDBPath string
	templateAPIKey string // plaintext proxy key baked into the template DB
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "integration-template-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create template dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	dbPath := filepath.Join(dir, "template.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create template store: %v\n", err)
		os.Exit(1)
	}

	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "migrate template store: %v\n", err)
		os.Exit(1)
	}

	plaintext, hash, prefix, err := auth.GenerateKey()
	if err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}

	if err := st.CreateProxyKey(context.Background(), "int-key", hash, prefix, "integration"); err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "create proxy key: %v\n", err)
		os.Exit(1)
	}

	if err := st.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close template store: %v\n", err)
		os.Exit(1)
	}

	templateDBPath = dbPath
	templateAPIKey = plaintext

	os.Exit(m.Run())
}

func cloneTestDB(t *testing.T) *store.SQLiteStore {
	t.Helper()

	src, err := os.ReadFile(templateDBPath) //nolint:gosec // controlled test fixture path set in TestMain
	if err != nil {
		t.Fatalf("read template DB: %v", err)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := os.WriteFile(dbPath, src, 0o600); err != nil { //nolint:gosec // dbPath is t.TempDir() + hardcoded filename
		t.Fatalf("write test DB: %v", err)
	}

	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	return st
}

// integrationHarness wires up a full proxy server backed by a mock upstream.
// It mirrors the setup in cmd/serve.go without the CLI layer.
type integrationHarness struct {
	upstream *httptest.Server
	proxy    *httptest.Server
	st       *store.SQLiteStore
	apiKey   string // plaintext proxy key for SDK auth
}

// newIntegrationHarness spins up an upstream mock, a full proxy (with auth
// middleware), and a SQLite store.  All resources are cleaned up via t.Cleanup.
func newIntegrationHarness(t *testing.T, upstreamHandler http.Handler) *integrationHarness {
	t.Helper()

	// --- mock upstream (Anthropic API) ---
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	// --- SQLite store (cloned from pre-migrated template) ---
	st := cloneTestDB(t)
	plaintext := templateAPIKey

	// --- config & providers ---
	cfg := &config.Config{
		MasterKey: "test-master",
		Listen:    ":0",
		Providers: []config.ProviderConfig{{ //nolint:gosec // #nosec G101 -- fake test credential
			Name:           "anthropic",
			BaseURL:        upstream.URL,
			AuthMode:       "proxy_key",
			APIKey:         "fake-upstream-key", //nolint:gosec // test credential, not real
			DefaultVersion: "2023-06-01",
		}},
		ModelList: []config.ModelMapping{
			// "claude-sonnet" is used by the Anthropic-SDK tests
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
			// "gpt-4" is used by the OpenAI-SDK tests
			{ModelName: "gpt-4", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
		},
	}
	anthropicClient, err := llmanthropic.NewClient(llmanthropic.ClientConfig{Name: "anthropic", BaseURL: upstream.URL, AuthMode: "proxy_key", APIKey: "fake-upstream-key", DefaultVersion: "2023-06-01"}) // #nosec G101 -- test credentials, not real secrets
	require.NoError(t, err)
	providers := map[string]provider.Provider{
		"anthropic": anthropicClient,
	}

	calc := pricing.NewCalculator(map[string]pricing.Entry{})
	logger := proxy.NewAsyncLogger(st, 100)
	t.Cleanup(func() { logger.Close() })

	proxyHandler := proxy.NewAnthropicHandler(cfg, providers, calc, logger, nil, nil, nil)
	openaiHandler := proxy.NewOpenAIHandler(cfg, providers, calc, logger, nil, nil, nil)

	// --- chi router (identical to serve.go) ---
	r := chi.NewRouter()
	r.Use(chimw.RealIP, chimw.Recoverer)
	r.Route("/v1", func(r chi.Router) {
		r.Use(proxy.AuthMiddleware(st))
		r.Post("/messages", proxyHandler.ServeHTTP)
		r.Post("/chat/completions", openaiHandler.ServeHTTP)
	})

	proxyServer := httptest.NewServer(r)
	t.Cleanup(proxyServer.Close)

	return &integrationHarness{
		upstream: upstream,
		proxy:    proxyServer,
		st:       st,
		apiKey:   plaintext,
	}
}

// anthropicClient returns an Anthropic SDK client pointed at the proxy.
// The proxy key is passed as X-Api-Key (the SDK's default header for Anthropic).
func (h *integrationHarness) anthropicClient() anthropicsdk.Client {
	return anthropicsdk.NewClient(
		anthropicoption.WithAPIKey(h.apiKey),
		anthropicoption.WithBaseURL(h.proxy.URL),
	)
}

// openaiClient returns an OpenAI SDK client pointed at the proxy's /v1 prefix.
// The proxy key is passed as Authorization: Bearer (OpenAI-style), which the
// proxy's AuthMiddleware accepts for keys starting with "llmp_sk_".
func (h *integrationHarness) openaiClient() openaisdk.Client {
	return openaisdk.NewClient(
		openaioption.WithAPIKey(h.apiKey),
		openaioption.WithBaseURL(h.proxy.URL+"/v1/"),
	)
}

// mockAnthropicJSON writes a minimal Anthropic Messages API JSON response.
func mockAnthropicJSON(w http.ResponseWriter, msgID, text string, inputTokens, outputTokens int64) {
	resp := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-sonnet-4-20250514",
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content":       []map[string]any{{"type": "text", "text": text}},
		"usage":         map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// buildAnthropicSSE returns a well-formed Anthropic SSE stream payload.
func buildAnthropicSSE(msgID string, inputTokens, outputTokens int64, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":%d,\"output_tokens\":0}}}\n\n",
		msgID, inputTokens,
	)
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	fmt.Fprintf(&b,
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n",
		text,
	)
	b.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	fmt.Fprintf(&b,
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n",
		outputTokens,
	)
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Anthropic Go SDK tests
// ---------------------------------------------------------------------------

func TestAnthropicSDK_NonStreaming(t *testing.T) {
	h := newIntegrationHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)

		// Verify the proxy translated the alias to the upstream model name.
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "claude-sonnet-4-20250514", body["model"])

		mockAnthropicJSON(w, "msg_sdk_001", "Hello from the proxy!", 42, 17)
	}))

	client := h.anthropicClient()
	msg, err := client.Messages.New(context.Background(), anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model("claude-sonnet"),
		MaxTokens: 100,
		Messages: []anthropicsdk.MessageParam{
			anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock("Hello, proxy!")),
		},
	})
	require.NoError(t, err)

	require.Equal(t, "msg_sdk_001", msg.ID)
	require.Len(t, msg.Content, 1)
	require.Equal(t, "text", msg.Content[0].Type)
	require.Equal(t, "Hello from the proxy!", msg.Content[0].Text)
	require.Equal(t, int64(42), msg.Usage.InputTokens)
	require.Equal(t, int64(17), msg.Usage.OutputTokens)
}

func TestAnthropicSDK_Streaming(t *testing.T) {
	const wantText = "Streaming response!"
	h := newIntegrationHarness(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, buildAnthropicSSE("msg_sdk_stream", 60, 30, wantText))
	}))

	client := h.anthropicClient()
	stream := client.Messages.NewStreaming(context.Background(), anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model("claude-sonnet"),
		MaxTokens: 100,
		Messages: []anthropicsdk.MessageParam{
			anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock("Stream this!")),
		},
	})

	var fullText strings.Builder
	for stream.Next() {
		event := stream.Current()
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fullText.WriteString(event.Delta.Text)
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, wantText, fullText.String())
}

func TestAnthropicSDK_AuthRejected(t *testing.T) {
	// Spin up a harness but use the wrong key — the proxy must return 401.
	h := newIntegrationHarness(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be reached when auth fails")
	}))

	badClient := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey("llmp_sk_000000000000badkey"),
		anthropicoption.WithBaseURL(h.proxy.URL),
	)
	_, err := badClient.Messages.New(context.Background(), anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model("claude-sonnet"),
		MaxTokens: 10,
		Messages:  []anthropicsdk.MessageParam{anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock("hi"))},
	})
	require.Error(t, err)
	// The SDK wraps the HTTP error; check the status code is present.
	require.Contains(t, err.Error(), "401")
}

// ---------------------------------------------------------------------------
// OpenAI Go SDK tests
// ---------------------------------------------------------------------------

func TestOpenAISDK_NonStreaming(t *testing.T) {
	h := newIntegrationHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)

		// The OpenAI handler translates gpt-4 → claude-sonnet-4-20250514.
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "claude-sonnet-4-20250514", body["model"])

		mockAnthropicJSON(w, "msg_oai_sdk_001", "OpenAI compat response!", 80, 40)
	}))

	client := h.openaiClient()
	completion, err := client.Chat.Completions.New(context.Background(), openaisdk.ChatCompletionNewParams{
		Model: "gpt-4",
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.UserMessage("Hello via OpenAI SDK!"),
		},
	})
	require.NoError(t, err)

	require.Equal(t, "chatcmpl-msg_oai_sdk_001", completion.ID)
	require.Len(t, completion.Choices, 1)
	require.Equal(t, "OpenAI compat response!", completion.Choices[0].Message.Content)
	require.Equal(t, "stop", completion.Choices[0].FinishReason)
	require.Equal(t, int64(80), completion.Usage.PromptTokens)
	require.Equal(t, int64(40), completion.Usage.CompletionTokens)
}

func TestOpenAISDK_Streaming(t *testing.T) {
	const wantText = "OpenAI streaming!"
	h := newIntegrationHarness(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, buildAnthropicSSE("msg_oai_stream", 90, 45, wantText))
	}))

	client := h.openaiClient()
	stream := client.Chat.Completions.NewStreaming(context.Background(), openaisdk.ChatCompletionNewParams{
		Model: "gpt-4",
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.UserMessage("Stream this!"),
		},
	})

	var fullText strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			fullText.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	require.NoError(t, stream.Err())
	require.Equal(t, wantText, fullText.String())
}

func TestOpenAISDK_SystemMessage(t *testing.T) {
	// Verify the proxy correctly extracts the system message from the OpenAI
	// messages array and places it in the Anthropic "system" field.
	h := newIntegrationHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		require.Equal(t, "You are a test assistant.", body["system"])

		msgs, ok := body["messages"].([]any)
		require.True(t, ok)
		// Only the user message should remain in the messages array.
		require.Len(t, msgs, 1)
		require.Equal(t, "user", msgs[0].(map[string]any)["role"])

		mockAnthropicJSON(w, "msg_sys_sdk", "Understood.", 30, 10)
	}))

	client := h.openaiClient()
	completion, err := client.Chat.Completions.New(context.Background(), openaisdk.ChatCompletionNewParams{
		Model: "gpt-4",
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage("You are a test assistant."),
			openaisdk.UserMessage("Hello!"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Understood.", completion.Choices[0].Message.Content)
}

func TestOpenAISDK_AuthRejected(t *testing.T) {
	h := newIntegrationHarness(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream must not be reached when auth fails")
	}))

	badClient := openaisdk.NewClient(
		openaioption.WithAPIKey("llmp_sk_000000000000badkey"),
		openaioption.WithBaseURL(h.proxy.URL+"/v1/"),
	)
	_, err := badClient.Chat.Completions.New(context.Background(), openaisdk.ChatCompletionNewParams{
		Model:    "gpt-4",
		Messages: []openaisdk.ChatCompletionMessageParamUnion{openaisdk.UserMessage("hi")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

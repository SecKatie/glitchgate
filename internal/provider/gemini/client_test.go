// SPDX-License-Identifier: AGPL-3.0-or-later

package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/seckatie/glitchgate/internal/provider"
)

// --- test helpers ---

// staticTokenSource returns a fixed token for testing.
type staticTokenSource struct {
	token string
}

func (s *staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: s.token,
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
	}, nil
}

// rewriteTransport rewrites Vertex AI URLs to point at a test server.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.target, "http://")
	if rt.base != nil {
		return rt.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// --- api_key mode tests ---

func TestClient_Interface_APIKey(t *testing.T) {
	client := &Client{authMode: "api_key"}
	require.Equal(t, "api_key", client.AuthMode())
	require.Equal(t, "gemini", client.APIFormat())
}

func TestSendRequest_APIKeyAuthHeader(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Goog-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "gemini-secret-key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		Headers:     http.Header{},
		Model:       "gemini-2.5-flash",
		IsStreaming: false,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "gemini-secret-key", gotAPIKey)
}

func TestSendRequest_URLConstructionStripsGooglePrefix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	_, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", gotPath)
}

func TestSendRequest_TokenExtraction_APIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4,"totalTokenCount":59}}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "gemini-2.5-flash",
		IsStreaming: false,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, int64(32), resp.InputTokens)
	require.Equal(t, int64(21), resp.OutputTokens)
	require.Equal(t, int64(10), resp.CacheReadInputTokens)
	require.Equal(t, int64(4), resp.ReasoningTokens)
}

func TestSendRequest_Streaming_APIKey(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"}}]}\n\n"))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "gemini-2.5-flash",
		IsStreaming: true,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Stream)
	require.True(t, strings.Contains(gotQuery, "alt=sse"))
	require.NoError(t, resp.Stream.Close())
}

func TestSendRequest_StreamingFallbacksToNonStreamingOnBadRequest(t *testing.T) {
	var sawStreamingPath bool
	var sawNonStreamingPath bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, ":streamGenerateContent"):
			sawStreamingPath = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"stream unsupported"}}`))
		case strings.Contains(r.URL.Path, ":generateContent"):
			sawNonStreamingPath = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "gemini-2.5-flash",
		IsStreaming: true,
	})
	require.NoError(t, err)
	require.True(t, sawStreamingPath)
	require.True(t, sawNonStreamingPath)
	require.Nil(t, resp.Stream)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int64(7), resp.InputTokens)
	require.Equal(t, int64(3), resp.OutputTokens)
	require.NotEmpty(t, resp.Body)
}

func TestEndpointURL_APIKey(t *testing.T) {
	client := &Client{authMode: "api_key"}
	require.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent",
		client.endpointURL("gemini-2.5-flash", false),
	)
}

// --- vertex mode tests ---

func TestClient_Interface_Vertex(t *testing.T) {
	client := &Client{authMode: "vertex"}
	require.Equal(t, "internal", client.AuthMode())
	require.Equal(t, "gemini", client.APIFormat())
}

func TestSendRequest_VertexAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "global"},
		&staticTokenSource{token: "gemini-secret-token"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "Bearer gemini-secret-token", gotAuth)
}

func TestSendRequest_VertexURLConstruction(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-central1"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	_, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", gotPath)
}

func TestSendRequest_VertexTokenExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4,"totalTokenCount":59}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "global"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, int64(32), resp.InputTokens)
	require.Equal(t, int64(21), resp.OutputTokens)
	require.Equal(t, int64(10), resp.CacheReadInputTokens)
	require.Equal(t, int64(4), resp.ReasoningTokens)
}

func TestSendRequest_VertexStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.String(), "streamGenerateContent?alt=sse"),
			"streaming URL should use streamGenerateContent?alt=sse, got: %s", r.URL.String())
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"}}]}\n\n"))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "global"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: true,
	}

	resp, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Stream, "streaming response should have Stream set")
	require.NoError(t, resp.Stream.Close())
}

func TestSendRequest_VertexDefaultRegion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	// Empty region should default to "us-central1".
	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	_, err := client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.True(t, strings.Contains(gotPath, "/locations/us-central1/"), "empty region should default to us-central1, got: %s", gotPath)
}

// --- ListModels tests ---

func TestListModels_Direct_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Contains(t, r.URL.Path, "/v1beta/models")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{"name": "models/gemini-2.5-flash", "displayName": "Gemini 2.5 Flash", "supportedGenerationMethods": ["generateContent", "countTokens"]},
				{"name": "models/gemini-2.0-pro", "displayName": "Gemini 2.0 Pro", "supportedGenerationMethods": ["generateContent"]}
			]
		}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "test-key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	models, err := client.ListModels(t.Context())
	require.NoError(t, err)
	require.Len(t, models, 2)

	// Verify "models/" prefix is stripped from the ID.
	require.Equal(t, "gemini-2.5-flash", models[0].ID)
	require.Equal(t, "Gemini 2.5 Flash", models[0].DisplayName)
	require.Equal(t, "gemini-2.0-pro", models[1].ID)
	require.Equal(t, "Gemini 2.0 Pro", models[1].DisplayName)
}

func TestListModels_Direct_FiltersEmbeddingModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{"name": "models/gemini-2.5-flash", "displayName": "Gemini 2.5 Flash", "supportedGenerationMethods": ["generateContent"]},
				{"name": "models/text-embedding-004", "displayName": "Text Embedding 004", "supportedGenerationMethods": ["embedContent"]},
				{"name": "models/embedding-001", "displayName": "Embedding 001", "supportedGenerationMethods": ["embedContent", "countTextTokens"]}
			]
		}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "test-key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	models, err := client.ListModels(t.Context())
	require.NoError(t, err)
	require.Len(t, models, 1, "embedding-only models should be filtered out")
	require.Equal(t, "gemini-2.5-flash", models[0].ID)
}

func TestListModels_Direct_Pagination(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		pageToken := r.URL.Query().Get("pageToken")
		if pageToken == "" {
			_, _ = w.Write([]byte(`{
				"models": [
					{"name": "models/gemini-2.5-flash", "displayName": "Gemini 2.5 Flash", "supportedGenerationMethods": ["generateContent"]}
				],
				"nextPageToken": "page2"
			}`))
		} else {
			require.Equal(t, "page2", pageToken)
			_, _ = w.Write([]byte(`{
				"models": [
					{"name": "models/gemini-2.0-pro", "displayName": "Gemini 2.0 Pro", "supportedGenerationMethods": ["generateContent"]}
				]
			}`))
		}
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "test-key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	models, err := client.ListModels(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "should have made two requests for two pages")
	require.Len(t, models, 2)
	require.Equal(t, "gemini-2.5-flash", models[0].ID)
	require.Equal(t, "gemini-2.0-pro", models[1].ID)
}

func TestListModels_Direct_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()

	client := newTestClient(ClientConfig{Name: "test", AuthMode: "api_key", APIKey: "bad-key"}, nil, srv.Client())
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	models, err := client.ListModels(t.Context())
	require.Error(t, err)
	require.Nil(t, models)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "API key not valid")
}

func TestListModels_Vertex_Success(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"publisherModels": [
				{"name": "publishers/google/models/gemini-2.0-flash"},
				{"name": "publishers/google/models/gemini-2.5-pro"}
			]
		}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-central1"},
		&staticTokenSource{token: "vertex-token"}, srv.Client(), //nolint:gosec // test credential
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	models, err := client.ListModels(t.Context())
	require.NoError(t, err)
	require.Equal(t, "Bearer vertex-token", gotAuth)
	require.Len(t, models, 2)

	// Verify publisher model names are parsed to extract just the model ID.
	require.Equal(t, "gemini-2.0-flash", models[0].ID)
	require.Equal(t, "gemini-2.5-pro", models[1].ID)
}

func TestSendRequest_VertexNoStreamingFallback(t *testing.T) {
	// Vertex mode should NOT retry on 400 (the streaming fallback is api_key only).
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-central1"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "gemini-2.5-flash",
		IsStreaming: true,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Equal(t, 1, requestCount, "vertex mode should not retry on 400")
}

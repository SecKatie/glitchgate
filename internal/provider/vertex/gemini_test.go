// SPDX-License-Identifier: AGPL-3.0-or-later

package vertex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider"
)

func TestGeminiClient_Interface(t *testing.T) {
	client := &GeminiClient{}
	require.Equal(t, "internal", client.AuthMode())
	require.Equal(t, "gemini", client.APIFormat())
}

func TestGeminiSendRequest_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	}))
	defer srv.Close()

	client := newTestGeminiClient("test", "my-project", "global",
		&staticTokenSource{token: "gemini-secret-token"}, srv.Client())
	client.httpClient.Transport = &rewriteTransport{
		base:   srv.Client().Transport,
		target: srv.URL,
	}

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

func TestGeminiSendRequest_URLConstruction(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	client := newTestGeminiClient("test", "my-project", "us-central1",
		&staticTokenSource{token: "tok"}, srv.Client())
	client.httpClient.Transport = &rewriteTransport{
		base:   srv.Client().Transport,
		target: srv.URL,
	}

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

func TestGeminiSendRequest_TokenExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4,"totalTokenCount":59}}`))
	}))
	defer srv.Close()

	client := newTestGeminiClient("test", "my-project", "global",
		&staticTokenSource{token: "tok"}, srv.Client())
	client.httpClient.Transport = &rewriteTransport{
		base:   srv.Client().Transport,
		target: srv.URL,
	}

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

func TestGeminiSendRequest_Streaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.String(), "streamGenerateContent?alt=sse"),
			"streaming URL should use streamGenerateContent?alt=sse, got: %s", r.URL.String())
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"}}]}\n\n"))
	}))
	defer srv.Close()

	client := newTestGeminiClient("test", "my-project", "global",
		&staticTokenSource{token: "tok"}, srv.Client())
	client.httpClient.Transport = &rewriteTransport{
		base:   srv.Client().Transport,
		target: srv.URL,
	}

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

func TestGeminiSendRequest_DefaultRegion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	// Empty region should default to "us-central1".
	client := newTestGeminiClient("test", "my-project", "",
		&staticTokenSource{token: "tok"}, srv.Client())
	client.httpClient.Transport = &rewriteTransport{
		base:   srv.Client().Transport,
		target: srv.URL,
	}

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

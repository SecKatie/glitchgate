// SPDX-License-Identifier: AGPL-3.0-or-later

package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider"
)

func TestClient_Interface(t *testing.T) {
	client := &Client{authMode: "proxy_key"}
	require.Equal(t, "proxy_key", client.AuthMode())
	require.Equal(t, "gemini", client.APIFormat())
}

func TestSendRequest_ProxyKeyAuthHeader(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Goog-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
	}))
	defer srv.Close()

	client, err := NewClient("test", srv.URL, "proxy_key", "gemini-secret-key")
	require.NoError(t, err)

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

func TestSendRequest_ForwardedAPIKey(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Goog-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	client, err := NewClient("test", srv.URL, "forward", "")
	require.NoError(t, err)

	req := &provider.Request{
		Body: []byte(`{"contents":[]}`),
		Headers: http.Header{
			"X-Goog-Api-Key": []string{"forwarded-key"},
		},
		Model:       "gemini-2.5-flash",
		IsStreaming: false,
	}

	_, err = client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "forwarded-key", gotAPIKey)
}

func TestSendRequest_URLConstructionStripsGooglePrefix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	client, err := NewClient("test", srv.URL, "proxy_key", "key")
	require.NoError(t, err)

	req := &provider.Request{
		Body:        []byte(`{"contents":[]}`),
		Headers:     http.Header{},
		Model:       "google/gemini-2.5-flash",
		IsStreaming: false,
	}

	_, err = client.SendRequest(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", gotPath)
}

func TestSendRequest_TokenExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4,"totalTokenCount":59}}`))
	}))
	defer srv.Close()

	client, err := NewClient("test", srv.URL, "proxy_key", "key")
	require.NoError(t, err)

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

func TestSendRequest_Streaming(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"}}]}\n\n"))
	}))
	defer srv.Close()

	client, err := NewClient("test", srv.URL, "proxy_key", "key")
	require.NoError(t, err)

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

	client, err := NewClient("test", srv.URL, "proxy_key", "key")
	require.NoError(t, err)

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

func TestEndpointURL_DefaultBaseURL(t *testing.T) {
	client := &Client{baseURL: DefaultBaseURL}
	require.Equal(t,
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent",
		client.endpointURL("gemini-2.5-flash", false),
	)
}

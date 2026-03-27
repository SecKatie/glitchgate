// SPDX-License-Identifier: AGPL-3.0-or-later

package anthropic

import (
	"context"
	"encoding/json"
	"io"
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

// --- proxy_key / forward mode tests ---

func TestSendRequest_ForwardsAnthropicAllowlistHeaders(t *testing.T) {
	t.Parallel()

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer srv.Close()

	client, err := NewClient(ClientConfig{Name: "claude-max", BaseURL: srv.URL + "/v1", AuthMode: "forward", DefaultVersion: "2023-06-01"})
	require.NoError(t, err)
	req := &provider.Request{
		Body:  []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`),
		Model: "claude-sonnet-4-6",
		Headers: http.Header{
			"Authorization":     []string{"Bearer secret"},
			"Anthropic-Version": []string{"2023-06-01"},
			"Anthropic-Beta":    []string{"messages-2023-12-15"},
			"User-Agent":        []string{"Claude-Code/Test"},
			"Accept":            []string{"application/json"},
			"X-App":             []string{"cli"},
			"X-Stainless-Lang":  []string{"js"},
			"X-Stainless-Os":    []string{"MacOS"},
			"X-Request-Id":      []string{"abc123"},
			"X-Proxy-Api-Key":   []string{"gg-secret"},
			"Connection":        []string{"keep-alive"},
		},
	}

	resp, err := client.SendRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Equal(t, "application/json", gotHeaders.Get("Content-Type"))
	require.Equal(t, "2023-06-01", gotHeaders.Get("Anthropic-Version"))
	require.Equal(t, "messages-2023-12-15", gotHeaders.Get("Anthropic-Beta"))
	require.Equal(t, "Bearer secret", gotHeaders.Get("Authorization"))
	require.Equal(t, "Claude-Code/Test", gotHeaders.Get("User-Agent"))
	require.Equal(t, "application/json", gotHeaders.Get("Accept"))
	require.NotContains(t, gotHeaders.Values("Accept-Encoding"), "br")
	require.Equal(t, "cli", gotHeaders.Get("X-App"))
	require.Equal(t, "js", gotHeaders.Get("X-Stainless-Lang"))
	require.Equal(t, "MacOS", gotHeaders.Get("X-Stainless-Os"))
	require.Empty(t, gotHeaders.Get("X-Request-Id"))
	require.Empty(t, gotHeaders.Get("X-Proxy-Api-Key"))
	require.Empty(t, gotHeaders.Get("Connection"))
}

func TestClient_Interface_Direct(t *testing.T) {
	client := &Client{authMode: "api_key"}
	require.Equal(t, "api_key", client.AuthMode())
	require.Equal(t, "anthropic", client.APIFormat())
}

func TestSendRequest_TokenExtraction_Direct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":17,"cache_creation_input_tokens":5,"cache_read_input_tokens":3}}`))
	}))
	defer srv.Close()

	client, err := NewClient(ClientConfig{Name: "test", BaseURL: srv.URL + "/v1", AuthMode: "api_key", APIKey: "key"})
	require.NoError(t, err)

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:    []byte(`{"model":"claude-sonnet-4-6","messages":[],"max_tokens":10}`),
		Model:   "claude-sonnet-4-6",
		Headers: http.Header{},
	})
	require.NoError(t, err)
	require.Equal(t, int64(42), resp.InputTokens)
	require.Equal(t, int64(17), resp.OutputTokens)
	require.Equal(t, int64(5), resp.CacheCreationInputTokens)
	require.Equal(t, int64(3), resp.CacheReadInputTokens)
}

// --- vertex mode tests ---

func TestClient_Interface_Vertex(t *testing.T) {
	client := &Client{authMode: "vertex"}
	require.Equal(t, "internal", client.AuthMode())
	require.Equal(t, "anthropic", client.APIFormat())
}

func TestSendRequest_VertexAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6-20250514","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
		&staticTokenSource{token: "my-secret-token"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[]}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: false,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "Bearer my-secret-token", gotAuth)
}

func TestSendRequest_VertexURLConstruction(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	_, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[]}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: false,
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-sonnet-4-6-20250514:rawPredict", gotPath)
}

func TestSendRequest_VertexStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, ":streamRawPredict"), "streaming should use streamRawPredict, got %s", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {}\n\n"))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[],"stream":true}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Stream)
	require.NoError(t, resp.Stream.Close())
}

func TestSendRequest_VertexTokenExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-sonnet-4-6-20250514","stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":17,"cache_creation_input_tokens":5,"cache_read_input_tokens":3}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	resp, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[]}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: false,
	})
	require.NoError(t, err)
	require.Equal(t, int64(42), resp.InputTokens)
	require.Equal(t, int64(17), resp.OutputTokens)
	require.Equal(t, int64(5), resp.CacheCreationInputTokens)
	require.Equal(t, int64(3), resp.CacheReadInputTokens)
}

func TestSendRequest_VertexBodyPreparation(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6-20250514","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer srv.Close()

	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	_, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: false,
	})
	require.NoError(t, err)

	var bodyMap map[string]any
	err = json.Unmarshal(gotBody, &bodyMap)
	require.NoError(t, err)

	_, hasModel := bodyMap["model"]
	require.False(t, hasModel, "model should be stripped from upstream body")
	require.Equal(t, defaultVertexVersion, bodyMap["anthropic_version"])
	require.Equal(t, float64(100), bodyMap["max_tokens"])
}

func TestSendRequest_VertexDefaultRegion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	// Empty region should default to "us-central1".
	client := newTestClient(
		ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project"},
		&staticTokenSource{token: "tok"}, srv.Client(),
	)
	client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	_, err := client.SendRequest(t.Context(), &provider.Request{
		Body:        []byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[]}`),
		Headers:     http.Header{},
		Model:       "claude-sonnet-4-6-20250514",
		IsStreaming: false,
	})
	require.NoError(t, err)
	require.True(t, strings.Contains(gotPath, "/locations/us-central1/"), "empty region should default to us-central1, got: %s", gotPath)
}

// --- prepareBody unit tests ---

func TestPrepareBody_StripsModel(t *testing.T) {
	result, err := prepareBody([]byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`), "2023-06-01")
	require.NoError(t, err)

	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(result, &bodyMap))
	_, hasModel := bodyMap["model"]
	require.False(t, hasModel, "model field should be stripped")
	require.Equal(t, float64(100), bodyMap["max_tokens"])
}

func TestPrepareBody_StripsUnsupportedFields(t *testing.T) {
	result, err := prepareBody([]byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[],"context_management":{"type":"ephemeral"}}`), "2023-06-01")
	require.NoError(t, err)

	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(result, &bodyMap))
	_, hasCtx := bodyMap["context_management"]
	require.False(t, hasCtx, "context_management should be stripped")
}

func TestPrepareBody_InjectsAnthropicVersion(t *testing.T) {
	result, err := prepareBody([]byte(`{"model":"claude-sonnet-4-6-20250514","max_tokens":100,"messages":[]}`), "2023-06-01")
	require.NoError(t, err)

	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(result, &bodyMap))
	require.Equal(t, "2023-06-01", bodyMap["anthropic_version"])
}

func TestPrepareBody_PreservesExistingAnthropicVersion(t *testing.T) {
	result, err := prepareBody([]byte(`{"model":"claude-sonnet-4-6-20250514","anthropic_version":"2024-01-01","max_tokens":100,"messages":[]}`), "2023-06-01")
	require.NoError(t, err)

	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(result, &bodyMap))
	require.Equal(t, "2024-01-01", bodyMap["anthropic_version"], "should not overwrite existing anthropic_version")
}

// --- ListModels tests ---

func TestListModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T) (*Client, *httptest.Server)
		wantModels []provider.DiscoveredModel
		wantErr    string
	}{
		{
			name: "direct_success_two_models",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					resp := modelsListResponse{
						Data: []modelInfo{
							{ID: "claude-sonnet-4-6-20250514", DisplayName: "Claude Sonnet 4.6"},
							{ID: "claude-haiku-3-5-20241022", DisplayName: "Claude 3.5 Haiku"},
						},
						HasMore: false,
					}
					b, _ := json.Marshal(resp)
					_, _ = w.Write(b)
				}))
				client, err := NewClient(ClientConfig{
					Name:           "test",
					BaseURL:        srv.URL + "/v1",
					AuthMode:       "api_key",
					APIKey:         "sk-test-key",
					DefaultVersion: "2023-06-01",
				})
				require.NoError(t, err)
				return client, srv
			},
			wantModels: []provider.DiscoveredModel{
				{ID: "claude-sonnet-4-6-20250514", DisplayName: "Claude Sonnet 4.6"},
				{ID: "claude-haiku-3-5-20241022", DisplayName: "Claude 3.5 Haiku"},
			},
		},
		{
			name: "direct_pagination",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				callCount := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callCount++
					w.Header().Set("Content-Type", "application/json")
					var resp modelsListResponse
					if callCount == 1 {
						require.Empty(t, r.URL.Query().Get("after_id"), "first request should not have after_id")
						resp = modelsListResponse{
							Data:    []modelInfo{{ID: "model-a", DisplayName: "Model A"}},
							HasMore: true,
							LastID:  "model-a",
						}
					} else {
						require.Equal(t, "model-a", r.URL.Query().Get("after_id"), "second request should paginate with after_id")
						resp = modelsListResponse{
							Data:    []modelInfo{{ID: "model-b", DisplayName: "Model B"}},
							HasMore: false,
						}
					}
					b, _ := json.Marshal(resp)
					_, _ = w.Write(b)
				}))
				client, err := NewClient(ClientConfig{
					Name:           "test",
					BaseURL:        srv.URL + "/v1",
					AuthMode:       "api_key",
					APIKey:         "sk-test-key",
					DefaultVersion: "2023-06-01",
				})
				require.NoError(t, err)
				return client, srv
			},
			wantModels: []provider.DiscoveredModel{
				{ID: "model-a", DisplayName: "Model A"},
				{ID: "model-b", DisplayName: "Model B"},
			},
		},
		{
			name: "direct_api_error",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
				}))
				client, err := NewClient(ClientConfig{ //nolint:gosec // test credential
					Name:           "test",
					BaseURL:        srv.URL + "/v1",
					AuthMode:       "api_key",
					APIKey:         "sk-bad-key", //nolint:gosec // test credential
					DefaultVersion: "2023-06-01",
				})
				require.NoError(t, err)
				return client, srv
			},
			wantErr: "models endpoint returned 403",
		},
		{
			name: "direct_headers",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "sk-verify-header", r.Header.Get("X-Api-Key"), "should send X-Api-Key header")
					require.Equal(t, "2023-06-01", r.Header.Get("Anthropic-Version"), "should send Anthropic-Version header")
					w.Header().Set("Content-Type", "application/json")
					resp := modelsListResponse{Data: []modelInfo{{ID: "claude-sonnet-4-6-20250514"}}, HasMore: false}
					b, _ := json.Marshal(resp)
					_, _ = w.Write(b)
				}))
				client, err := NewClient(ClientConfig{ //nolint:gosec // test credential
					Name:           "test",
					BaseURL:        srv.URL + "/v1",
					AuthMode:       "api_key",
					APIKey:         "sk-verify-header", //nolint:gosec // test credential
					DefaultVersion: "2023-06-01",
				})
				require.NoError(t, err)
				return client, srv
			},
			wantModels: []provider.DiscoveredModel{
				{ID: "claude-sonnet-4-6-20250514"},
			},
		},
		{
			name: "direct_empty_default_version_uses_fallback",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.NotEmpty(t, r.Header.Get("Anthropic-Version"), "should send a non-empty Anthropic-Version even when DefaultVersion is empty")
					w.Header().Set("Content-Type", "application/json")
					resp := modelsListResponse{Data: []modelInfo{{ID: "claude-sonnet-4-6-20250514"}}, HasMore: false}
					b, _ := json.Marshal(resp)
					_, _ = w.Write(b)
				}))
				client, err := NewClient(ClientConfig{
					Name:     "test",
					BaseURL:  srv.URL + "/v1",
					AuthMode: "api_key",
					APIKey:   "sk-test-key",
					// DefaultVersion intentionally empty
				})
				require.NoError(t, err)
				return client, srv
			},
			wantModels: []provider.DiscoveredModel{
				{ID: "claude-sonnet-4-6-20250514"},
			},
		},
		{
			name: "vertex_success",
			setup: func(t *testing.T) (*Client, *httptest.Server) {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "Bearer vertex-token", r.Header.Get("Authorization"))
					w.Header().Set("Content-Type", "application/json")
					resp := vertexModelsListResponse{
						PublisherModels: []vertexPublisherModel{
							{Name: "publishers/anthropic/models/claude-sonnet-4-6"},
							{Name: "publishers/anthropic/models/claude-haiku-3-5"},
						},
					}
					b, _ := json.Marshal(resp)
					_, _ = w.Write(b)
				}))
				client := newTestClient(
					ClientConfig{Name: "test", AuthMode: "vertex", Project: "my-project", Region: "us-east5"},
					&staticTokenSource{token: "vertex-token"}, srv.Client(), //nolint:gosec // test credential
				)
				client.httpClient.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}
				return client, srv
			},
			wantModels: []provider.DiscoveredModel{
				{ID: "claude-sonnet-4-6"},
				{ID: "claude-haiku-3-5"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, srv := tt.setup(t)
			defer srv.Close()

			models, err := client.ListModels(t.Context())

			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantModels, models)
		})
	}
}

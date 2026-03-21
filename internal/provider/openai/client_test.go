// SPDX-License-Identifier: AGPL-3.0-or-later

package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider"
)

func TestClient(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Name",
			run: func(t *testing.T) {
				c, err := NewClient("my-openai", "http://localhost", "proxy_key", "sk-test", "")
				require.NoError(t, err)
				require.Equal(t, "my-openai", c.Name())
			},
		},
		{
			name: "AuthMode",
			run: func(t *testing.T) {
				c, err := NewClient("oai", "http://localhost", "forward", "", "")
				require.NoError(t, err)
				require.Equal(t, "forward", c.AuthMode())
			},
		},
		{
			name: "APIFormat_ChatCompletions",
			run: func(t *testing.T) {
				c, err := NewClient("oai", "http://localhost", "proxy_key", "sk-test", APITypeChatCompletions)
				require.NoError(t, err)
				require.Equal(t, "openai", c.APIFormat())
			},
		},
		{
			name: "APIFormat_Responses",
			run: func(t *testing.T) {
				c, err := NewClient("oai", "http://localhost", "proxy_key", "sk-test", APITypeResponses)
				require.NoError(t, err)
				require.Equal(t, "responses", c.APIFormat())
			},
		},
		{
			name: "APIFormat_Default",
			run: func(t *testing.T) {
				c, err := NewClient("oai", "http://localhost", "proxy_key", "sk-test", "")
				require.NoError(t, err)
				require.Equal(t, "openai", c.APIFormat())
			},
		},
		{
			name: "SendRequest_ForwardsOpenAIAllowlistHeaders",
			run: func(t *testing.T) {
				var gotHeaders http.Header
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotHeaders = r.Header.Clone()
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"usage":{"input_tokens":1,"output_tokens":2}}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "forward", "", APITypeResponses)
				require.NoError(t, err)
				hdr := http.Header{
					"Authorization":         []string{"Bearer client-token-xyz"},
					"Accept":                []string{"application/json"},
					"Accept-Language":       []string{"en-US"},
					"Chatgpt-Account-Id":    []string{"acct_123"},
					"Originator":            []string{"codex_cli_rs"},
					"Session_id":            []string{"sess_123"},
					"User-Agent":            []string{"Codex/1.0"},
					"OpenAI-Beta":           []string{"assistants=v2"},
					"OpenAI-Organization":   []string{"org_123"},
					"OpenAI-Project":        []string{"proj_123"},
					"X-App":                 []string{"codex"},
					"X-Codex-Beta-Features": []string{"js_repl,multi_agent,apps"},
					"X-Codex-Turn-Metadata": []string{`{"turn_id":"turn_123","sandbox":"seatbelt"}`},
					"X-Stainless-Lang":      []string{"js"},
					"X-Stainless-OS":        []string{"MacOS"},
					"X-Proxy-Api-Key":       []string{"llmp_sk_secret"},
					"X-Request-Id":          []string{"req_123"},
					"Connection":            []string{"keep-alive"},
				}
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4.1"}`),
					Headers: hdr,
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)

				require.Equal(t, "application/json", gotHeaders.Get("Content-Type"))
				require.Equal(t, "Bearer client-token-xyz", gotHeaders.Get("Authorization"))
				require.Equal(t, "application/json", gotHeaders.Get("Accept"))
				require.Equal(t, "en-US", gotHeaders.Get("Accept-Language"))
				require.Equal(t, "acct_123", gotHeaders.Get("Chatgpt-Account-Id"))
				require.Equal(t, "codex_cli_rs", gotHeaders.Get("Originator"))
				require.Equal(t, "sess_123", gotHeaders.Get("Session_id"))
				require.Equal(t, "Codex/1.0", gotHeaders.Get("User-Agent"))
				require.Equal(t, "assistants=v2", gotHeaders.Get("OpenAI-Beta"))
				require.Equal(t, "org_123", gotHeaders.Get("OpenAI-Organization"))
				require.Equal(t, "proj_123", gotHeaders.Get("OpenAI-Project"))
				require.Equal(t, "codex", gotHeaders.Get("X-App"))
				require.Equal(t, "js_repl,multi_agent,apps", gotHeaders.Get("X-Codex-Beta-Features"))
				require.Equal(t, `{"turn_id":"turn_123","sandbox":"seatbelt"}`, gotHeaders.Get("X-Codex-Turn-Metadata"))
				require.Equal(t, "js", gotHeaders.Get("X-Stainless-Lang"))
				require.Equal(t, "MacOS", gotHeaders.Get("X-Stainless-Os"))
				require.Empty(t, gotHeaders.Get("X-Proxy-Api-Key"))
				require.Empty(t, gotHeaders.Get("X-Request-Id"))
				require.Empty(t, gotHeaders.Get("Connection"))
			},
		},
		{
			name: "SendRequest_ChatCompletions_ProxyKey",
			run: func(t *testing.T) {
				var gotPath, gotAuth string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					gotAuth = r.Header.Get("Authorization")
					w.Header().Set("Content-Type", "application/json")
					resp := map[string]any{
						"usage": map[string]any{
							"prompt_tokens":     10,
							"completion_tokens": 20,
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-secret", APITypeChatCompletions)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, "/v1/chat/completions", gotPath)
				require.Equal(t, "Bearer sk-secret", gotAuth)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, int64(10), resp.InputTokens)
				require.Equal(t, int64(20), resp.OutputTokens)
			},
		},
		{
			name: "SendRequest_Responses_ProxyKey",
			run: func(t *testing.T) {
				var gotPath, gotAuth string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					gotAuth = r.Header.Get("Authorization")
					w.Header().Set("Content-Type", "application/json")
					resp := map[string]any{
						"usage": map[string]any{
							"input_tokens":  100,
							"output_tokens": 50,
							"input_tokens_details": map[string]any{
								"cached_tokens": 30,
							},
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-resp", APITypeResponses)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, "/v1/responses", gotPath)
				require.Equal(t, "Bearer sk-resp", gotAuth)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, int64(70), resp.InputTokens)
				require.Equal(t, int64(50), resp.OutputTokens)
				require.Equal(t, int64(30), resp.CacheReadInputTokens)
			},
		},
		{
			name: "SendRequest_Responses_CustomPathBaseURL",
			run: func(t *testing.T) {
				var gotPath string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"usage": map[string]any{
							"input_tokens":  10,
							"output_tokens": 5,
						},
					})
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL+"/backend-api/codex", "proxy_key", "sk-custom", APITypeResponses)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				_, err = c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, "/backend-api/codex/responses", gotPath)
			},
		},
		{
			name: "SendRequest_ChatCompletions_BaseURLAlreadyHasV1",
			run: func(t *testing.T) {
				var gotPath string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"usage": map[string]any{
							"prompt_tokens":     10,
							"completion_tokens": 20,
						},
					})
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL+"/v1", "proxy_key", "sk-v1", APITypeChatCompletions)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				_, err = c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, "/v1/chat/completions", gotPath)
			},
		},
		{
			name: "SendRequest_Forward_Auth",
			run: func(t *testing.T) {
				var gotAuth string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotAuth = r.Header.Get("Authorization")
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "forward", "", APITypeChatCompletions)
				require.NoError(t, err)
				hdr := http.Header{}
				hdr.Set("Authorization", "Bearer client-token-xyz")
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: hdr,
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, "Bearer client-token-xyz", gotAuth)
				require.Equal(t, http.StatusOK, resp.StatusCode)
			},
		},
		{
			name: "SendRequest_Streaming",
			run: func(t *testing.T) {
				streamBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(streamBody))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
				require.NoError(t, err)
				req := &provider.Request{
					Body:        []byte(`{"model":"gpt-4","stream":true}`),
					Headers:     http.Header{},
					IsStreaming: true,
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.NotNil(t, resp.Stream, "streaming response should have Stream set")
				require.Nil(t, resp.Body, "streaming response should not have Body set")

				data, err := io.ReadAll(resp.Stream)
				require.NoError(t, err)
				_ = resp.Stream.Close()
				require.Equal(t, streamBody, string(data))

				// Token extraction does not happen for streaming.
				require.Equal(t, int64(0), resp.InputTokens)
				require.Equal(t, int64(0), resp.OutputTokens)
			},
		},
		{
			name: "SendRequest_TokenExtraction_CC",
			run: func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{
						"id": "chatcmpl-abc",
						"choices": [{"message": {"content": "hello"}}],
						"usage": {
							"prompt_tokens": 42,
							"completion_tokens": 17,
							"total_tokens": 59
						}
					}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, int64(42), resp.InputTokens)
				require.Equal(t, int64(17), resp.OutputTokens)
				require.Equal(t, int64(0), resp.CacheCreationInputTokens)
				require.Equal(t, int64(0), resp.CacheReadInputTokens)
			},
		},
		{
			name: "SendRequest_TokenExtraction_Responses",
			run: func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{
						"id": "resp-abc",
						"output": [{"type":"message","content":[{"text":"hi"}]}],
						"usage": {
							"input_tokens": 200,
							"output_tokens": 80,
							"input_tokens_details": {
								"cached_tokens": 150
							}
						}
					}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeResponses)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, int64(50), resp.InputTokens)
				require.Equal(t, int64(80), resp.OutputTokens)
				require.Equal(t, int64(150), resp.CacheReadInputTokens)
				require.Equal(t, int64(0), resp.CacheCreationInputTokens)
			},
		},
		{
			name: "SendRequest_Error",
			run: func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
				require.NoError(t, err)
				req := &provider.Request{
					Body:    []byte(`{"model":"gpt-4"}`),
					Headers: http.Header{},
				}

				resp, err := c.SendRequest(context.Background(), req)
				require.NoError(t, err)
				require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
				// Tokens must NOT be extracted for non-200 responses.
				require.Equal(t, int64(0), resp.InputTokens)
				require.Equal(t, int64(0), resp.OutputTokens)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestListModels(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Success_ThreeModels",
			run: func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/v1/models", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					resp := map[string]any{
						"data": []map[string]any{
							{"id": "gpt-4", "owned_by": "openai"},
							{"id": "gpt-4o", "owned_by": "openai"},
							{"id": "gpt-3.5-turbo", "owned_by": "openai-internal"},
						},
					}
					_ = json.NewEncoder(w).Encode(resp)
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", "")
				require.NoError(t, err)

				models, err := c.ListModels(context.Background())
				require.NoError(t, err)
				require.Len(t, models, 3)
				require.Equal(t, provider.DiscoveredModel{ID: "gpt-4"}, models[0])
				require.Equal(t, provider.DiscoveredModel{ID: "gpt-4o"}, models[1])
				require.Equal(t, provider.DiscoveredModel{ID: "gpt-3.5-turbo"}, models[2])
			},
		},
		{
			name: "APIError_Non200",
			run: func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-bad", "")
				require.NoError(t, err)

				models, err := c.ListModels(context.Background())
				require.Error(t, err)
				require.Nil(t, models)
				require.Contains(t, err.Error(), "401")
			},
		},
		{
			name: "AuthorizationBearerHeader_ProxyKey",
			run: func(t *testing.T) {
				var gotAuth string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotAuth = r.Header.Get("Authorization")
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":[]}`))
				}))
				defer srv.Close()

				c, err := NewClient("oai", srv.URL, "proxy_key", "sk-verify-auth", "")
				require.NoError(t, err)

				_, err = c.ListModels(context.Background())
				require.NoError(t, err)
				require.Equal(t, "Bearer sk-verify-auth", gotAuth)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

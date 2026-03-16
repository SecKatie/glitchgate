package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestSendRequest_ForwardsAnthropicAllowlistHeaders(t *testing.T) {
	t.Parallel()

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer srv.Close()

	client := NewClient("claude-max", srv.URL, "forward", "", "2023-06-01")
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

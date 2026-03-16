package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider"
)

func TestClientSendRequestAppliesDefaultTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	client, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
	require.NoError(t, err)
	client.SetTimeouts(10 * time.Millisecond)

	start := time.Now()
	_, err = client.SendRequest(context.Background(), &provider.Request{
		Body:    []byte(`{"model":"gpt-4.1"}`),
		Headers: http.Header{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")
	require.Less(t, time.Since(start), 250*time.Millisecond)
}

func TestClientSendRequestPreservesCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	client, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
	require.NoError(t, err)
	client.SetTimeouts(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.SendRequest(ctx, &provider.Request{
		Body:    []byte(`{"model":"gpt-4.1"}`),
		Headers: http.Header{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")
	require.Less(t, time.Since(start), 250*time.Millisecond)
}

func TestStreamingRequestHasNoContextDeadline(t *testing.T) {
	// Streaming requests must not have a context deadline applied by the client.
	// The caller's context (if any) is respected, but no additional deadline is
	// added. Once headers are received the stream stays open indefinitely.
	var deadlineSet bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hello\n\n"))
	}))
	defer srv.Close()

	client, err := NewClient("oai", srv.URL, "proxy_key", "sk-test", APITypeChatCompletions)
	require.NoError(t, err)
	client.SetTimeouts(time.Second)

	resp, err := client.SendRequest(context.Background(), &provider.Request{
		Body:        []byte(`{"model":"gpt-4.1","stream":true}`),
		Headers:     http.Header{},
		IsStreaming: true,
	})
	require.NoError(t, err)
	_ = resp.Stream.Close()
	require.False(t, deadlineSet, "streaming request should not inject a context deadline")
}

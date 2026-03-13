package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/proxy"
	"codeberg.org/kglitchy/glitchgate/internal/ratelimit"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

func TestAnthropicProxy_RejectsOversizedBody(t *testing.T) {
	var hitUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitUpstream = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := newTestHarness(t, upstream.URL)
	h.cfg.ProxyMaxBodyBytes = 64

	reqBody := `{"model":"claude-sonnet","messages":[{"role":"user","content":"` + strings.Repeat("x", 256) + `"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/messages", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.False(t, hitUpstream)
	require.Contains(t, rec.Body.String(), "invalid_request_error")
}

func TestOpenAIProxy_RejectsOversizedBody(t *testing.T) {
	var hitUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitUpstream = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := newOpenAITestHarness(t, upstream.URL)
	h.cfg.ProxyMaxBodyBytes = 64

	reqBody := `{"model":"gpt-4","messages":[{"role":"user","content":"` + strings.Repeat("x", 256) + `"}],"max_tokens":100}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/chat/completions", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.False(t, hitUpstream)
	require.Contains(t, rec.Body.String(), "invalid_request_error")
}

func TestResponsesProxy_RejectsOversizedBody(t *testing.T) {
	var hitUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitUpstream = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := newResponsesTestHarness(t, upstream.URL)
	h.cfg.ProxyMaxBodyBytes = 64

	reqBody := `{"model":"gpt-4o","input":"` + strings.Repeat("x", 256) + `"}`
	req := h.buildAuthenticatedRequest(t, http.MethodPost, "/v1/responses", reqBody)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.False(t, hitUpstream)
	require.Contains(t, rec.Body.String(), "invalid_request_error")
}

func TestProxyIPRateLimitMiddleware(t *testing.T) {
	limiter := ratelimit.New(1, 1, 15*time.Minute)
	handler := proxy.IPRateLimitMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	first.RemoteAddr = "203.0.113.10:1234"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	require.Equal(t, http.StatusOK, firstRec.Code)

	second := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	second.RemoteAddr = "203.0.113.10:4321"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	require.Contains(t, secondRec.Body.String(), "rate_limit_error")
}

func TestProxyKeyRateLimitMiddleware(t *testing.T) {
	limiter := ratelimit.New(1, 1, 15*time.Minute)
	handler := proxy.KeyRateLimitMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		req.RemoteAddr = "203.0.113.20:9876"
		ctx := proxy.ContextWithProxyKey(req.Context(), &store.ProxyKey{ID: "pk-1"})
		return req.WithContext(ctx)
	}

	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, makeReq())
	require.Equal(t, http.StatusOK, firstRec.Code)

	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, makeReq())
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	require.Contains(t, secondRec.Body.String(), "rate_limit_error")
}

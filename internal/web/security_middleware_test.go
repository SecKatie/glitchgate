package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/ratelimit"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	handler := SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	require.Equal(t, referrerPolicyValue, rec.Header().Get("Referrer-Policy"))
	require.Equal(t, permissionsPolicyValue, rec.Header().Get("Permissions-Policy"))
}

func TestLoginRateLimitMiddlewareJSON(t *testing.T) {
	limiter := ratelimit.New(1, 1, 15*time.Minute)
	handler := LoginRateLimitMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodPost, "/ui/api/login", nil)
	first.Header.Set("Content-Type", "application/json")
	first.RemoteAddr = "198.51.100.40:1111"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	require.Equal(t, http.StatusOK, firstRec.Code)

	second := httptest.NewRequest(http.MethodPost, "/ui/api/login", nil)
	second.Header.Set("Content-Type", "application/json")
	second.RemoteAddr = "198.51.100.40:2222"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	require.Contains(t, secondRec.Body.String(), "Too many login attempts")
}

package web

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/ratelimit"
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

	csp := rec.Header().Get("Content-Security-Policy")
	require.Contains(t, csp, "default-src 'self'")
	require.Contains(t, csp, "frame-ancestors 'none'")
	require.Contains(t, csp, "base-uri 'self'")
	require.Contains(t, csp, "form-action 'self'")
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

func TestCSRFMiddleware_GETSetsCookie(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == csrfCookieName {
			found = true
			require.NotEmpty(t, c.Value)
			require.Equal(t, "/ui", c.Path)
			require.Equal(t, http.SameSiteStrictMode, c.SameSite)
		}
	}
	require.True(t, found, "CSRF cookie should be set on GET")
}

func TestCSRFMiddleware_POSTWithoutToken(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ui/api/keys", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "CSRF")
}

func TestCSRFMiddleware_POSTWithValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler := CSRFMiddleware(inner)

	tokenValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	req := httptest.NewRequest(http.MethodPost, "/ui/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tokenValue})
	req.Header.Set(csrfHeaderName, tokenValue)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

func TestIsRequestTLS(t *testing.T) {
	tests := []struct {
		name     string
		tlsConn  bool
		xfpValue string
		want     bool
	}{
		{"direct TLS", true, "", true},
		{"no TLS no header", false, "", false},
		{"X-Forwarded-Proto https", false, "https", true},
		{"X-Forwarded-Proto HTTPS", false, "HTTPS", true},
		{"X-Forwarded-Proto http", false, "http", false},
		{"direct TLS with header", true, "http", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.tlsConn {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.xfpValue != "" {
				req.Header.Set("X-Forwarded-Proto", tt.xfpValue)
			}
			require.Equal(t, tt.want, isRequestTLS(req))
		})
	}
}

func TestCSRFCookieSecureViaProxy(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			require.True(t, c.Secure, "CSRF cookie should be Secure when X-Forwarded-Proto is https")
			return
		}
	}
	t.Fatal("CSRF cookie not found")
}

func TestCSRFMiddleware_POSTWithMismatchedToken(t *testing.T) {
	handler := CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ui/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookie-value"})
	req.Header.Set(csrfHeaderName, "different-header-value")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

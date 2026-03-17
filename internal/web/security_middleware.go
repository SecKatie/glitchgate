package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/seckatie/glitchgate/internal/ratelimit"
)

const (
	permissionsPolicyValue = "accelerometer=(), camera=(), geolocation=(), gyroscope=(), microphone=(), payment=(), usb=()"
	referrerPolicyValue    = "no-referrer"
)

// SecurityHeadersMiddleware adds a baseline browser-facing security header set.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if h.Get("X-Content-Type-Options") == "" {
			h.Set("X-Content-Type-Options", "nosniff")
		}
		if h.Get("X-Frame-Options") == "" {
			h.Set("X-Frame-Options", "DENY")
		}
		if h.Get("Referrer-Policy") == "" {
			h.Set("Referrer-Policy", referrerPolicyValue)
		}
		if h.Get("Permissions-Policy") == "" {
			h.Set("Permissions-Policy", permissionsPolicyValue)
		}
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

// LoginRateLimitMiddleware limits repeated login attempts by remote IP.
func LoginRateLimitMiddleware(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := remoteAddrKey(r.RemoteAddr)
			if l == nil || l.Allow(ip) {
				next.ServeHTTP(w, r)
				return
			}
			// #nosec G706 -- path and IP are normalized via safeLogValue before logging.
			slog.Warn("login rate limit exceeded", "path", safeLogValue(r.URL.Path), "remote_ip", safeLogValue(ip))
			writeLoginRateLimitError(w, r)
		})
	}
}

func writeLoginRateLimitError(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Too many login attempts"})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("Too many login attempts"))
}

func remoteAddrKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
}

func safeLogValue(value string) string {
	return strconv.QuoteToASCII(strings.ToValidUTF8(value, ""))
}

const (
	csrfCookieName = "glitchgate_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfTokenBytes = 32
)

// CSRFMiddleware implements the double-submit cookie pattern.
// On GET/HEAD/OPTIONS it ensures a CSRF cookie is set.
// On POST/PUT/DELETE it validates the X-CSRF-Token header matches the cookie.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			ensureCSRFCookie(w, r)
			next.ServeHTTP(w, r)
		default:
			cookie, err := r.Cookie(csrfCookieName)
			if err != nil || cookie.Value == "" {
				writeCSRFError(w, r)
				return
			}
			header := r.Header.Get(csrfHeaderName)
			if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
				writeCSRFError(w, r)
				return
			}
			next.ServeHTTP(w, r)
		}
	})
}

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(csrfCookieName); err == nil {
		return // cookie already exists
	}
	raw := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		slog.Error("generate CSRF token", "error", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    hex.EncodeToString(raw),
		Path:     "/ui",
		HttpOnly: false, // JS/HTMX must read the cookie value
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
}

func writeCSRFError(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "application/json" ||
		r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"CSRF token missing or invalid"}`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("CSRF token missing or invalid"))
}

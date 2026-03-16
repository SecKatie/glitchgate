package web

import (
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

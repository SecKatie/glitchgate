package proxy

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/ratelimit"
)

type errorWriter func(http.ResponseWriter, int, string, string)

func readRequestBodyWithLimit(w http.ResponseWriter, r *http.Request, maxBytes int, code string, writeErr errorWriter) ([]byte, bool) {
	limit := maxBytes
	if limit <= 0 {
		limit = config.DefaultProxyMaxBodyBytes
	}

	r.Body = http.MaxBytesReader(w, r.Body, int64(limit))

	body, err := io.ReadAll(r.Body)
	if err == nil {
		return body, true
	}

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeErr(w, http.StatusRequestEntityTooLarge, code, fmt.Sprintf("Request body exceeds %d bytes", limit))
		return nil, false
	}

	writeErr(w, http.StatusBadRequest, code, "Failed to read request body")
	return nil, false
}

// IPRateLimitMiddleware limits requests using the caller IP address.
func IPRateLimitMiddleware(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := remoteAddrKey(r.RemoteAddr)
			if l != nil && !l.Allow(ip) {
				// #nosec G706 -- path and IP are normalized via safeLogValue before logging.
				slog.Warn("proxy rate limit exceeded", "limiter", "ip", "path", safeLogValue(r.URL.Path), "remote_ip", safeLogValue(ip))
				writeProxyRateLimitError(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyRateLimitMiddleware limits authenticated proxy traffic by proxy key ID.
func KeyRateLimitMiddleware(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l == nil {
				next.ServeHTTP(w, r)
				return
			}

			pk := KeyFromContext(r.Context())
			key := "unknown"
			if pk != nil && pk.ID != "" {
				key = pk.ID
			}

			if !l.Allow(key) {
				// #nosec G706 -- logged values are normalized via safeLogValue before logging.
				slog.Warn("proxy rate limit exceeded",
					"limiter", "proxy_key",
					"key", safeLogValue(key),
					"path", safeLogValue(r.URL.Path),
					"remote_ip", safeLogValue(remoteAddrKey(r.RemoteAddr)),
				)
				writeProxyRateLimitError(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyAwareRateLimitMiddleware limits authenticated proxy traffic using both
// the global rate limit and any per-key rate limit overrides.
func KeyAwareRateLimitMiddleware(kl *KeyAwareRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if kl == nil {
				next.ServeHTTP(w, r)
				return
			}

			pk := KeyFromContext(r.Context())
			key := "unknown"
			if pk != nil && pk.ID != "" {
				key = pk.ID
			}

			if !kl.Allow(r.Context(), key) {
				// #nosec G706 -- logged values are normalized via safeLogValue before logging.
				slog.Warn("proxy rate limit exceeded",
					"limiter", "proxy_key",
					"key", safeLogValue(key),
					"path", safeLogValue(r.URL.Path),
					"remote_ip", safeLogValue(remoteAddrKey(r.RemoteAddr)),
				)
				writeProxyRateLimitError(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeProxyRateLimitError(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded")
	case strings.HasSuffix(r.URL.Path, "/responses"):
		writeResponsesError(w, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded")
	default:
		writeAnthropicError(w, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded")
	}
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

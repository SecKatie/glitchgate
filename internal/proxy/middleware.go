package proxy

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

type contextKey string

const proxyKeyCtxKey contextKey = "proxy_key"

// KeyFromContext retrieves the authenticated proxy key from the request context.
func KeyFromContext(ctx context.Context) *store.ProxyKey {
	if k, ok := ctx.Value(proxyKeyCtxKey).(*store.ProxyKey); ok {
		return k
	}
	return nil
}

// ContextWithProxyKey returns a new context with the proxy key attached.
// This is useful for testing handlers without going through the auth middleware.
func ContextWithProxyKey(ctx context.Context, pk *store.ProxyKey) context.Context {
	return context.WithValue(ctx, proxyKeyCtxKey, pk)
}

// AuthMiddleware validates the proxy API key on every request.
// It checks x-api-key and x-proxy-api-key headers, verifies the key
// against stored hashes, and injects the key into the request context.
func AuthMiddleware(s store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-Api-Key")
			if apiKey == "" {
				apiKey = r.Header.Get("X-Proxy-Api-Key")
			}
			// Also check Authorization: Bearer for OpenAI-style clients.
			if apiKey == "" {
				if bearer := r.Header.Get("Authorization"); len(bearer) > 7 && bearer[:7] == "Bearer " {
					candidate := bearer[7:]
					// Only treat as proxy key if it starts with our prefix.
					if len(candidate) > 8 && candidate[:8] == "llmp_sk_" {
						apiKey = candidate
					}
				}
			}

			if apiKey == "" {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Missing proxy API key")
				return
			}

			// Extract prefix to look up the key.
			if len(apiKey) < 12 {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid proxy API key")
				return
			}
			prefix := apiKey[:12]

			pk, err := s.GetActiveProxyKeyByPrefix(r.Context(), prefix)
			if err != nil {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid proxy API key")
				return
			}

			if !auth.VerifyKey(apiKey, pk.KeyHash) {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid proxy API key")
				return
			}

			ctx := context.WithValue(r.Context(), proxyKeyCtxKey, pk)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": message,
		},
	}); err != nil {
		log.Printf("WARNING: write error response: %v", err)
	}
}

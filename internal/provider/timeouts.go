package provider

import (
	"context"
	"net"
	"net/http"
	"time"
)

const (
	defaultDialTimeout           = 10 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 5 * time.Minute
	defaultIdleConnTimeout       = 90 * time.Second
)

// BuildHTTPClient returns an HTTP client with conservative transport-level
// timeouts that are safe for both streaming and non-streaming upstream calls.
func BuildHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   defaultDialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       defaultIdleConnTimeout,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ExpectContinueTimeout: time.Second,
			ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		},
	}
}

// ContextWithDefaultTimeout preserves the caller deadline when present and
// otherwise applies a default timeout for the upstream request.
func ContextWithDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	// #nosec G118 -- the cancel func is returned to the caller to manage.
	return context.WithTimeout(ctx, timeout)
}

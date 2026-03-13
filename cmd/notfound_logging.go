// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"log/slog"
	"net/http"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func warnOnNotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		if ww.Status() != http.StatusNotFound {
			return
		}

		slog.Warn("request returned 404", // #nosec G706 -- slog handlers escape newlines in values; newline injection not possible
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"remote_addr", r.RemoteAddr,
		)
	})
}

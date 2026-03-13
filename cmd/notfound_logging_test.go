// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestWarnOnNotFound_UnmatchedRoute(t *testing.T) {
	record := exerciseWarnOnNotFound(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}), httptest.NewRequest(http.MethodGet, "/missing?foo=bar", nil))

	require.Equal(t, "WARN", record["level"])
	require.Equal(t, "request returned 404", record["msg"])
	require.Equal(t, "GET", record["method"])
	require.Equal(t, "/missing", record["path"])
	require.Equal(t, "foo=bar", record["query"])
}

func TestWarnOnNotFound_ChiDefaultNotFound(t *testing.T) {
	r := chi.NewRouter()
	r.Use(warnOnNotFound)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	record := captureSingleLogRecord(t, r, httptest.NewRequest(http.MethodGet, "/unknown", nil))

	require.Equal(t, "WARN", record["level"])
	require.Equal(t, "GET", record["method"])
	require.Equal(t, "/unknown", record["path"])
}

func TestWarnOnNotFound_IgnoresNon404(t *testing.T) {
	var buf bytes.Buffer
	restore := replaceDefaultLogger(&buf)
	defer restore()

	handler := warnOnNotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Empty(t, bytes.TrimSpace(buf.Bytes()))
}

func exerciseWarnOnNotFound(t *testing.T, next http.Handler, req *http.Request) map[string]any {
	t.Helper()

	return captureSingleLogRecord(t, warnOnNotFound(next), req)
}

func captureSingleLogRecord(t *testing.T, handler http.Handler, req *http.Request) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	restore := replaceDefaultLogger(&buf)
	defer restore()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 1)

	var record map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &record))
	return record
}

func replaceDefaultLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	return func() {
		slog.SetDefault(prev)
	}
}

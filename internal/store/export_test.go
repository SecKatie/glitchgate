package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Set up source database with test data.
	srcPath := t.TempDir() + "/source.db"
	src, err := NewSQLiteStore(srcPath)
	require.NoError(t, err)
	defer func() { _ = src.Close() }()
	require.NoError(t, src.Migrate(ctx))

	// Insert a proxy key.
	err = src.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_test", "test key")
	require.NoError(t, err)

	// Insert a request log.
	err = src.InsertRequestLog(ctx, &RequestLogEntry{
		ID:             "log-1",
		ProxyKeyID:     "key-1",
		Timestamp:      time.Now().UTC(),
		SourceFormat:   "anthropic",
		ProviderName:   "test-provider",
		ModelRequested: "claude-3",
		ModelUpstream:  "claude-3",
		InputTokens:    100,
		OutputTokens:   50,
		LatencyMs:      200,
		Status:         200,
		RequestBody:    `{"test": true}`,
		ResponseBody:   `{"ok": true}`,
	})
	require.NoError(t, err)

	// Insert an audit event.
	err = src.RecordAuditEvent(ctx, "key_created", "sk_test", "test key", "cli")
	require.NoError(t, err)

	// Export.
	var buf bytes.Buffer
	err = src.Export(ctx, &buf)
	require.NoError(t, err)

	// Verify gzip format (magic bytes).
	exported := buf.Bytes()
	require.True(t, len(exported) >= 2 && exported[0] == 0x1f && exported[1] == 0x8b,
		"export should be gzip-compressed")

	// Verify SQL content inside the gzip.
	gz, err := gzip.NewReader(bytes.NewReader(exported))
	require.NoError(t, err)
	sqlBytes, err := io.ReadAll(gz)
	require.NoError(t, err)
	sqlContent := string(sqlBytes)
	require.Contains(t, sqlContent, "-- version: 1")
	require.Contains(t, sqlContent, "INSERT OR IGNORE INTO proxy_keys")
	require.Contains(t, sqlContent, "INSERT OR IGNORE INTO request_logs")

	// Import into a fresh database.
	dstPath := t.TempDir() + "/dest.db"
	dst, err := NewSQLiteStore(dstPath)
	require.NoError(t, err)
	defer func() { _ = dst.Close() }()
	require.NoError(t, dst.Migrate(ctx))

	stats, err := dst.Import(ctx, bytes.NewReader(exported))
	require.NoError(t, err)

	// Verify stats.
	var totalRows int64
	for _, s := range stats.Tables {
		totalRows += s.Rows
	}
	require.GreaterOrEqual(t, totalRows, int64(3)) // at least key + log + audit

	// Verify the key made it.
	key, err := dst.GetActiveProxyKeyByPrefix(ctx, "sk_test")
	require.NoError(t, err)
	require.Equal(t, "test key", key.Label)

	// Verify the log made it.
	log, err := dst.GetRequestLog(ctx, "log-1")
	require.NoError(t, err)
	require.Equal(t, "claude-3", log.ModelRequested)
	require.Equal(t, int64(100), log.InputTokens)
}

func TestExportImportWithNewlines(t *testing.T) {
	ctx := context.Background()

	srcPath := t.TempDir() + "/source.db"
	src, err := NewSQLiteStore(srcPath)
	require.NoError(t, err)
	defer func() { _ = src.Close() }()
	require.NoError(t, src.Migrate(ctx))

	err = src.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_nl", "test key")
	require.NoError(t, err)

	// Insert a log with multi-line JSON bodies.
	bodyWithNewlines := "{\n  \"messages\": [\n    {\"role\": \"user\", \"content\": \"hello\\nworld\"}\n  ]\n}"
	err = src.InsertRequestLog(ctx, &RequestLogEntry{
		ID:             "log-nl",
		ProxyKeyID:     "key-1",
		Timestamp:      time.Now().UTC(),
		SourceFormat:   "anthropic",
		ProviderName:   "test-provider",
		ModelRequested: "claude-3",
		ModelUpstream:  "claude-3",
		InputTokens:    10,
		OutputTokens:   5,
		LatencyMs:      100,
		Status:         200,
		RequestBody:    bodyWithNewlines,
		ResponseBody:   bodyWithNewlines,
	})
	require.NoError(t, err)

	// Export and re-import.
	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	dstPath := t.TempDir() + "/dest.db"
	dst, err := NewSQLiteStore(dstPath)
	require.NoError(t, err)
	defer func() { _ = dst.Close() }()
	require.NoError(t, dst.Migrate(ctx))

	_, err = dst.Import(ctx, &buf)
	require.NoError(t, err)

	// Verify the newlines survived the round trip.
	log, err := dst.GetRequestLog(ctx, "log-nl")
	require.NoError(t, err)
	require.Equal(t, bodyWithNewlines, log.RequestBody)
	require.Equal(t, bodyWithNewlines, log.ResponseBody)
}

func TestImportSkipsDuplicates(t *testing.T) {
	ctx := context.Background()

	dbPath := t.TempDir() + "/test.db"
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	require.NoError(t, st.Migrate(ctx))

	// Insert a key first.
	err = st.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_dup", "original label")
	require.NoError(t, err)

	// Build a gzip'd SQL dump with a conflicting key.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	fmt.Fprintln(gz, "-- glitchgate database export")
	fmt.Fprintln(gz, "-- version: 1")
	fmt.Fprintf(gz, "-- exported_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(gz)
	fmt.Fprintf(gz, "INSERT OR IGNORE INTO proxy_keys (id, key_hash, key_prefix, label, created_at) VALUES ('key-1', 'hash-xyz', 'sk_dup', 'imported label', '%s');\n",
		time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, gz.Close())

	stats, err := st.Import(ctx, &buf)
	require.NoError(t, err)

	// Key should not have been imported (duplicate PK).
	var keyRows int64
	for _, s := range stats.Tables {
		if s.Table == "proxy_keys" {
			keyRows = s.Rows
		}
	}
	require.Equal(t, int64(0), keyRows)

	// Original label should be preserved.
	key, err := st.GetActiveProxyKeyByPrefix(ctx, "sk_dup")
	require.NoError(t, err)
	require.Equal(t, "original label", key.Label)
}

func TestSqlLiteral(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, "NULL"},
		{"int64", int64(42), "42"},
		{"int64 negative", int64(-1), "-1"},
		{"float64", float64(3.14), "3.14"},
		{"float64 zero", float64(0), "0"},
		{"string", "hello", "'hello'"},
		{"string with quote", "it's", "'it''s'"},
		{"empty string", "", "''"},
		{"bool true", true, "1"},
		{"bool false", false, "0"},
		{"bytes", []byte{0xde, 0xad}, "X'dead'"},
		{"string with newline", "line1\nline2", "'line1' || char(10) || 'line2'"},
		{"string with crlf", "a\r\nb", "'a' || char(13,10) || 'b'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sqlLiteral(tt.input))
		})
	}
}

func TestExtractTableName(t *testing.T) {
	require.Equal(t, "proxy_keys",
		extractTableName("INSERT OR IGNORE INTO proxy_keys (id, key_hash) VALUES ('a', 'b');"))
	require.Equal(t, "request_logs",
		extractTableName("INSERT OR IGNORE INTO request_logs (id) VALUES ('x');"))
	require.Equal(t, "unknown", extractTableName("-- comment"))
	require.Equal(t, "unknown", extractTableName(""))
}

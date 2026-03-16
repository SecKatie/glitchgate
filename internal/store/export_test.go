package store

import (
	"bytes"
	"context"
	"encoding/json"
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

	// Verify JSON structure.
	var data ExportData
	err = json.Unmarshal(buf.Bytes(), &data)
	require.NoError(t, err)
	require.Equal(t, 1, data.Version)
	require.Len(t, data.ProxyKeys, 1)
	require.Len(t, data.RequestLogs, 1)
	require.Len(t, data.AuditEvents, 1)

	// Import into a fresh database.
	dstPath := t.TempDir() + "/dest.db"
	dst, err := NewSQLiteStore(dstPath)
	require.NoError(t, err)
	defer func() { _ = dst.Close() }()
	require.NoError(t, dst.Migrate(ctx))

	stats, err := dst.Import(ctx, &buf)
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

	// Create export data with the same key ID but different label.
	data := ExportData{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Tables:     exportTables,
		ProxyKeys: []map[string]any{
			{
				"id":         "key-1",
				"key_hash":   "hash-xyz",
				"key_prefix": "sk_dup",
				"label":      "imported label",
				"created_at": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	buf, err := json.Marshal(data)
	require.NoError(t, err)

	stats, err := st.Import(ctx, bytes.NewReader(buf))
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

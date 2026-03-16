package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrimRequestLogBodies(t *testing.T) {
	ctx := context.Background()

	dbPath := t.TempDir() + "/trim.db"
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	require.NoError(t, st.Migrate(ctx))

	// Create a proxy key for FK.
	err = st.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_trim", "trim test")
	require.NoError(t, err)

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour) // 30 days ago
	recent := now.Add(-1 * time.Hour)     // 1 hour ago

	// Insert an old log and a recent log.
	err = st.InsertRequestLog(ctx, &RequestLogEntry{
		ID: "old-1", ProxyKeyID: "key-1", Timestamp: old,
		SourceFormat: "anthropic", ProviderName: "test", ModelRequested: "m1", ModelUpstream: "m1",
		InputTokens: 100, OutputTokens: 50, LatencyMs: 200, Status: 200,
		RequestBody: `{"old": "request"}`, ResponseBody: `{"old": "response"}`,
	})
	require.NoError(t, err)

	err = st.InsertRequestLog(ctx, &RequestLogEntry{
		ID: "recent-1", ProxyKeyID: "key-1", Timestamp: recent,
		SourceFormat: "anthropic", ProviderName: "test", ModelRequested: "m1", ModelUpstream: "m1",
		InputTokens: 100, OutputTokens: 50, LatencyMs: 200, Status: 200,
		RequestBody: `{"recent": "request"}`, ResponseBody: `{"recent": "response"}`,
	})
	require.NoError(t, err)

	// Trim logs older than 7 days.
	cutoff := now.Add(-7 * 24 * time.Hour)
	trimmed, err := st.TrimRequestLogBodies(ctx, cutoff, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1), trimmed)

	// Old log should have trimmed bodies.
	oldLog, err := st.GetRequestLog(ctx, "old-1")
	require.NoError(t, err)
	require.Equal(t, "[trimmed]", oldLog.RequestBody)
	require.Equal(t, "[trimmed]", oldLog.ResponseBody)
	// Metadata should be preserved.
	require.Equal(t, int64(100), oldLog.InputTokens)
	require.Equal(t, int64(50), oldLog.OutputTokens)

	// Recent log should be untouched.
	recentLog, err := st.GetRequestLog(ctx, "recent-1")
	require.NoError(t, err)
	require.Equal(t, `{"recent": "request"}`, recentLog.RequestBody)
	require.Equal(t, `{"recent": "response"}`, recentLog.ResponseBody)
}

func TestTrimIdempotent(t *testing.T) {
	ctx := context.Background()

	dbPath := t.TempDir() + "/trim_idem.db"
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	require.NoError(t, st.Migrate(ctx))

	err = st.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_idem", "idem test")
	require.NoError(t, err)

	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	err = st.InsertRequestLog(ctx, &RequestLogEntry{
		ID: "old-1", ProxyKeyID: "key-1", Timestamp: old,
		SourceFormat: "anthropic", ProviderName: "test", ModelRequested: "m1", ModelUpstream: "m1",
		InputTokens: 10, OutputTokens: 5, LatencyMs: 100, Status: 200,
		RequestBody: `{"body": true}`, ResponseBody: `{"resp": true}`,
	})
	require.NoError(t, err)

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)

	// First trim.
	n1, err := st.TrimRequestLogBodies(ctx, cutoff, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1), n1)

	// Second trim should be a no-op.
	n2, err := st.TrimRequestLogBodies(ctx, cutoff, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(0), n2)
}

func TestCountTrimmableLogBodies(t *testing.T) {
	ctx := context.Background()

	dbPath := t.TempDir() + "/trim_count.db"
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	require.NoError(t, st.Migrate(ctx))

	err = st.CreateProxyKey(ctx, "key-1", "hash-abc", "sk_cnt", "count test")
	require.NoError(t, err)

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)

	for i := 0; i < 5; i++ {
		err = st.InsertRequestLog(ctx, &RequestLogEntry{
			ID: fmt.Sprintf("log-%d", i), ProxyKeyID: "key-1", Timestamp: old,
			SourceFormat: "anthropic", ProviderName: "test", ModelRequested: "m1", ModelUpstream: "m1",
			InputTokens: 10, OutputTokens: 5, LatencyMs: 100, Status: 200,
			RequestBody: `{"x": true}`, ResponseBody: `{"y": true}`,
		})
		require.NoError(t, err)
	}

	cutoff := now.Add(-7 * 24 * time.Hour)
	count, err := st.CountTrimmableLogBodies(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(5), count)

	// Trim them all.
	_, err = st.TrimRequestLogBodies(ctx, cutoff, 1000)
	require.NoError(t, err)

	// Count should now be zero.
	count, err = st.CountTrimmableLogBodies(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
}

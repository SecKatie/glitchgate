// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// seedLogsForSince inserts a proxy key and four request logs with predictable
// timestamps for CountLogsSince tests.
//
// Insertion order (chronological):
//
//	log-s1  2026-03-10T09:00  model=claude-sonnet  status=200
//	log-s2  2026-03-10T10:00  model=claude-sonnet  status=200
//	log-s3  2026-03-10T11:00  model=claude-opus    status=200
//	log-s4  2026-03-10T12:00  model=claude-opus    status=429
func seedLogsForSince(t *testing.T, st *SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, st.CreateProxyKey(ctx, "pk-s1", "hash-s1", "llmp_sk_ss11", "since-key"))

	entries := []RequestLogEntry{
		{
			ID: "log-s1", ProxyKeyID: "pk-s1",
			Timestamp:    time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 200, LatencyMs: 500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-s2", ProxyKeyID: "pk-s1",
			Timestamp:    time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 50, OutputTokens: 100, LatencyMs: 300, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-s3", ProxyKeyID: "pk-s1",
			Timestamp:    time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 200, OutputTokens: 400, LatencyMs: 800, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-s4", ProxyKeyID: "pk-s1",
			Timestamp:    time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 10, OutputTokens: 20, LatencyMs: 200, Status: 429,
			RequestBody: "{}", ResponseBody: "{}",
		},
	}
	for i := range entries {
		require.NoError(t, st.InsertRequestLog(ctx, &entries[i]))
	}
}

func TestCountLogsSince(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	seedLogsForSince(t, st)
	ctx := context.Background()

	t.Run("empty sinceID returns 0", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "", ListLogsParams{})
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})

	t.Run("unknown sinceID returns 0", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "does-not-exist", ListLogsParams{})
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})

	t.Run("since first entry counts three newer", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "log-s1", ListLogsParams{})
		require.NoError(t, err)
		require.Equal(t, int64(3), count)
	})

	t.Run("since second entry counts two newer", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "log-s2", ListLogsParams{})
		require.NoError(t, err)
		require.Equal(t, int64(2), count)
	})

	t.Run("since last entry counts zero newer", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "log-s4", ListLogsParams{})
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})

	t.Run("since first entry filtered by model", func(t *testing.T) {
		// log-s1 is anchor; newer entries with model=claude-opus are log-s3, log-s4 → 2
		count, err := st.CountLogsSince(ctx, "log-s1", ListLogsParams{
			Model: "claude-opus",
		})
		require.NoError(t, err)
		require.Equal(t, int64(2), count)
	})

	t.Run("since first entry filtered by status", func(t *testing.T) {
		// Only log-s4 is status=429 and newer than log-s1 → 1
		count, err := st.CountLogsSince(ctx, "log-s1", ListLogsParams{
			Status: 429,
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
	})

	t.Run("since first entry filtered by key prefix", func(t *testing.T) {
		// All newer entries belong to the same key → 3
		count, err := st.CountLogsSince(ctx, "log-s1", ListLogsParams{
			KeyPrefix: "llmp_sk_ss11",
		})
		require.NoError(t, err)
		require.Equal(t, int64(3), count)
	})

	t.Run("since first entry with non-matching key prefix returns 0", func(t *testing.T) {
		count, err := st.CountLogsSince(ctx, "log-s1", ListLogsParams{
			KeyPrefix: "llmp_sk_zzxx",
		})
		require.NoError(t, err)
		require.Equal(t, int64(0), count)
	})
}

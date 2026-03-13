package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPruneRequestLogsDeletesOldRowsInBatches(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, st.CreateProxyKey(ctx, "pk-1", "hash-1", "llmp_sk_aa11", "key-alpha"))

	logs := []RequestLogEntry{
		{
			ID:             "old-1",
			ProxyKeyID:     "pk-1",
			Timestamp:      time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat:   "anthropic",
			ProviderName:   "anthropic",
			ModelRequested: "claude-sonnet",
			ModelUpstream:  "claude-sonnet-4-20250514",
			LatencyMs:      100,
			Status:         200,
			RequestBody:    "{}",
			ResponseBody:   "{}",
		},
		{
			ID:             "old-2",
			ProxyKeyID:     "pk-1",
			Timestamp:      time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC),
			SourceFormat:   "anthropic",
			ProviderName:   "anthropic",
			ModelRequested: "claude-sonnet",
			ModelUpstream:  "claude-sonnet-4-20250514",
			LatencyMs:      100,
			Status:         200,
			RequestBody:    "{}",
			ResponseBody:   "{}",
		},
		{
			ID:             "new-1",
			ProxyKeyID:     "pk-1",
			Timestamp:      time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC),
			SourceFormat:   "anthropic",
			ProviderName:   "anthropic",
			ModelRequested: "claude-sonnet",
			ModelUpstream:  "claude-sonnet-4-20250514",
			LatencyMs:      100,
			Status:         200,
			RequestBody:    "{}",
			ResponseBody:   "{}",
		},
	}

	for i := range logs {
		require.NoError(t, st.InsertRequestLog(ctx, &logs[i]))
	}

	cutoff := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)

	deleted, err := st.PruneRequestLogs(ctx, cutoff, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	deleted, err = st.PruneRequestLogs(ctx, cutoff, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	deleted, err = st.PruneRequestLogs(ctx, cutoff, 1)
	require.NoError(t, err)
	require.Equal(t, int64(0), deleted)

	_, err = st.GetRequestLog(ctx, "old-1")
	require.Error(t, err)
	_, err = st.GetRequestLog(ctx, "old-2")
	require.Error(t, err)

	logEntry, err := st.GetRequestLog(ctx, "new-1")
	require.NoError(t, err)
	require.NotNil(t, logEntry)
}

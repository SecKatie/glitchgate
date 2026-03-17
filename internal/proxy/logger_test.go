package proxy

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/store"
)

type capturingStore struct {
	delay   time.Duration
	mu      sync.Mutex
	entries []*store.RequestLogEntry
}

func (s *capturingStore) InsertRequestLog(ctx context.Context, entry *store.RequestLogEntry) error {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	copyEntry := *entry
	s.entries = append(s.entries, &copyEntry)
	return nil
}

func (s *capturingStore) lastEntry() *store.RequestLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return nil
	}
	return s.entries[len(s.entries)-1]
}

func TestAsyncLoggerTimeoutsAreCounted(t *testing.T) {
	st := &capturingStore{delay: 50 * time.Millisecond}
	logger := NewAsyncLoggerWithOptions(st, AsyncLoggerOptions{
		BufferSize:      1,
		WriteTimeout:    10 * time.Millisecond,
		EnqueueTimeout:  5 * time.Millisecond,
		SummaryInterval: time.Hour,
		BodyMaxBytes:    64,
	})

	logger.Log(&store.RequestLogEntry{ID: "log-timeout"})
	logger.Close()

	stats := logger.Stats()
	require.Equal(t, uint64(1), stats.Enqueued)
	require.Equal(t, uint64(0), stats.Persisted)
	require.Equal(t, uint64(1), stats.TimedOut)
}

func TestAsyncLoggerDropsWhenBufferStaysFull(t *testing.T) {
	st := &capturingStore{delay: 75 * time.Millisecond}
	logger := NewAsyncLoggerWithOptions(st, AsyncLoggerOptions{
		BufferSize:      1,
		WriteTimeout:    time.Second,
		EnqueueTimeout:  5 * time.Millisecond,
		SummaryInterval: time.Hour,
		BodyMaxBytes:    64,
	})

	logger.Log(&store.RequestLogEntry{ID: "log-1"})
	logger.Log(&store.RequestLogEntry{ID: "log-2"})
	logger.Log(&store.RequestLogEntry{ID: "log-3"})
	logger.Close()

	stats := logger.Stats()
	require.Equal(t, uint64(1), stats.Dropped)
	require.Equal(t, uint64(2), stats.Persisted)
}

func TestAsyncLoggerTruncatesBodiesBeforePersisting(t *testing.T) {
	st := &capturingStore{}
	logger := NewAsyncLoggerWithOptions(st, AsyncLoggerOptions{
		BufferSize:      1,
		WriteTimeout:    time.Second,
		EnqueueTimeout:  time.Second,
		SummaryInterval: time.Hour,
		BodyMaxBytes:    24,
	})

	logger.logEntry(
		"proxy-key",
		"",
		"anthropic",
		"anthropic",
		"claude-sonnet",
		"claude-sonnet-4-20250514",
		"",
		25,
		[]byte(`{"api_key":"secret","payload":"`+strings.Repeat("x", 64)+`"}`),
		1,
		handlerResult{
			Status: 200,
			Body:   []byte(strings.Repeat("y", 64)),
		},
		nil,
	)
	logger.Close()

	entry := st.lastEntry()
	require.NotNil(t, entry)
	require.NotContains(t, entry.RequestBody, "secret")
	require.Contains(t, entry.RequestBody, "[TRUNCATED original_bytes=")
	require.Contains(t, entry.ResponseBody, "[TRUNCATED original_bytes=")
}

func TestAsyncLoggerConcurrentLogAndClose(t *testing.T) {
	st := &capturingStore{}
	logger := NewAsyncLoggerWithOptions(st, AsyncLoggerOptions{
		BufferSize:      10,
		WriteTimeout:    time.Second,
		EnqueueTimeout:  5 * time.Millisecond,
		SummaryInterval: time.Hour,
		BodyMaxBytes:    64,
	})

	// Hammer Log() from many goroutines while Close() is called concurrently.
	// Before the fix, this would panic with "send on closed channel".
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				logger.Log(&store.RequestLogEntry{ID: "race-test"})
			}
		}()
	}

	// Let some entries queue up, then close while goroutines are still sending.
	time.Sleep(2 * time.Millisecond)
	logger.Close()
	wg.Wait()

	// No panic means the race is fixed. Verify stats are consistent.
	stats := logger.Stats()
	require.Equal(t, stats.Enqueued+stats.Dropped, uint64(1000))
}

package proxy

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/store"
)

// AsyncLoggerOptions configures the async request log writer.
type AsyncLoggerOptions struct {
	BufferSize      int
	WriteTimeout    time.Duration
	EnqueueTimeout  time.Duration
	SummaryInterval time.Duration
	BodyMaxBytes    int
}

// AsyncLoggerStats is a point-in-time snapshot of logger activity.
type AsyncLoggerStats struct {
	Enqueued  uint64
	Persisted uint64
	Dropped   uint64
	Failed    uint64
	TimedOut  uint64
}

// AsyncLogger writes request log entries to the store asynchronously
// to avoid blocking the proxy path.
type AsyncLogger struct {
	ch              chan *store.RequestLogEntry
	store           store.RequestLogWriter
	done            chan struct{}
	stopCh          chan struct{} // closed first by Close(); signals Log() to stop sending
	writeTimeout    time.Duration
	enqueueTimeout  time.Duration
	summaryInterval time.Duration
	bodyMaxBytes    int
	closeOnce       sync.Once
	enqueued        atomic.Uint64
	persisted       atomic.Uint64
	dropped         atomic.Uint64
	failed          atomic.Uint64
	timedOut        atomic.Uint64
}

// NewAsyncLogger creates and starts an async log writer.
func NewAsyncLogger(s store.RequestLogWriter, bufSize int) *AsyncLogger {
	return NewAsyncLoggerWithOptions(s, AsyncLoggerOptions{
		BufferSize:      bufSize,
		WriteTimeout:    config.DefaultAsyncLogWriteTimeout,
		EnqueueTimeout:  100 * time.Millisecond,
		SummaryInterval: time.Minute,
		BodyMaxBytes:    config.DefaultRequestLogBodyMaxBytes,
	})
}

// NewAsyncLoggerWithOptions creates and starts an async log writer with explicit options.
func NewAsyncLoggerWithOptions(s store.RequestLogWriter, opts AsyncLoggerOptions) *AsyncLogger {
	if opts.BufferSize <= 0 {
		opts.BufferSize = config.DefaultAsyncLogBufferSize
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = config.DefaultAsyncLogWriteTimeout
	}
	if opts.EnqueueTimeout <= 0 {
		opts.EnqueueTimeout = 100 * time.Millisecond
	}
	if opts.SummaryInterval <= 0 {
		opts.SummaryInterval = time.Minute
	}
	if opts.BodyMaxBytes <= 0 {
		opts.BodyMaxBytes = config.DefaultRequestLogBodyMaxBytes
	}

	l := &AsyncLogger{
		ch:              make(chan *store.RequestLogEntry, opts.BufferSize),
		store:           s,
		done:            make(chan struct{}),
		stopCh:          make(chan struct{}),
		writeTimeout:    opts.WriteTimeout,
		enqueueTimeout:  opts.EnqueueTimeout,
		summaryInterval: opts.SummaryInterval,
		bodyMaxBytes:    opts.BodyMaxBytes,
	}
	go l.run()
	return l
}

// Log enqueues a log entry for async persistence.
func (l *AsyncLogger) Log(entry *store.RequestLogEntry) {
	timer := time.NewTimer(l.enqueueTimeout)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case l.ch <- entry:
		l.enqueued.Add(1)
	case <-l.stopCh:
		l.noteDropped(entry.ID, "logger closed")
	case <-timer.C:
		l.noteDropped(entry.ID, "log buffer full")
	}
}

// Close signals the logger to drain remaining entries and stop.
func (l *AsyncLogger) Close() {
	l.closeOnce.Do(func() {
		close(l.stopCh)
		<-l.done
	})
}

// Stats returns a snapshot of async logger counters.
func (l *AsyncLogger) Stats() AsyncLoggerStats {
	return AsyncLoggerStats{
		Enqueued:  l.enqueued.Load(),
		Persisted: l.persisted.Load(),
		Dropped:   l.dropped.Load(),
		Failed:    l.failed.Load(),
		TimedOut:  l.timedOut.Load(),
	}
}

func (l *AsyncLogger) run() {
	defer close(l.done)
	ticker := time.NewTicker(l.summaryInterval)
	defer ticker.Stop()

	for {
		select {
		case entry := <-l.ch:
			if entry != nil {
				l.persistEntry(entry)
			}
		case <-l.stopCh:
			// Drain any remaining entries that were enqueued before Close().
			for {
				select {
				case entry := <-l.ch:
					if entry != nil {
						l.persistEntry(entry)
					}
				default:
					l.logSummary()
					return
				}
			}
		case <-ticker.C:
			l.logSummary()
		}
	}
}

func (l *AsyncLogger) persistEntry(entry *store.RequestLogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), l.writeTimeout)
	defer cancel()

	if err := l.store.InsertRequestLog(ctx, entry); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			count := l.timedOut.Add(1)
			if count == 1 || count%100 == 0 {
				slog.Warn("request log persistence timed out", "entry_id", entry.ID, "count", count)
			}
			return
		}

		count := l.failed.Add(1)
		if count == 1 || count%100 == 0 {
			slog.Warn("failed to persist log entry", "entry_id", entry.ID, "count", count, "error", err)
		}
		return
	}

	l.persisted.Add(1)
}

func (l *AsyncLogger) noteDropped(entryID, reason string) {
	count := l.dropped.Add(1)
	if count == 1 || count%100 == 0 {
		slog.Warn("dropping request log entry", "entry_id", entryID, "reason", reason, "count", count)
	}
}

func (l *AsyncLogger) logSummary() {
	stats := l.Stats()
	if stats.Dropped == 0 && stats.Failed == 0 && stats.TimedOut == 0 {
		return
	}
	slog.Warn("async logger summary",
		"enqueued", stats.Enqueued,
		"persisted", stats.Persisted,
		"dropped", stats.Dropped,
		"failed", stats.Failed,
		"timed_out", stats.TimedOut,
	)
}

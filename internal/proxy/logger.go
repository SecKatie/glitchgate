package proxy

import (
	"context"
	"log/slog"

	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// AsyncLogger writes request log entries to the store asynchronously
// to avoid blocking the proxy path.
type AsyncLogger struct {
	ch    chan *store.RequestLogEntry
	store store.Store
	done  chan struct{}
}

// NewAsyncLogger creates and starts an async log writer.
func NewAsyncLogger(s store.Store, bufSize int) *AsyncLogger {
	l := &AsyncLogger{
		ch:    make(chan *store.RequestLogEntry, bufSize),
		store: s,
		done:  make(chan struct{}),
	}
	go l.run()
	return l
}

// Log enqueues a log entry for async persistence.
// If the channel is full, the entry is dropped with a warning.
func (l *AsyncLogger) Log(entry *store.RequestLogEntry) {
	select {
	case l.ch <- entry:
	default:
		slog.Warn("log buffer full, dropping entry", "entry_id", entry.ID)
	}
}

// Close signals the logger to drain remaining entries and stop.
func (l *AsyncLogger) Close() {
	close(l.ch)
	<-l.done
}

func (l *AsyncLogger) run() {
	defer close(l.done)
	for entry := range l.ch {
		if err := l.store.InsertRequestLog(context.Background(), entry); err != nil {
			slog.Warn("failed to persist log entry", "entry_id", entry.ID, "error", err)
		}
	}
}

package proxy

import (
	"context"
	"log"

	"codeberg.org/kglitchy/llm-proxy/internal/store"
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
		log.Printf("WARNING: log buffer full, dropping entry %s", entry.ID)
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
			log.Printf("WARNING: failed to persist log entry %s: %v", entry.ID, err)
		}
	}
}

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// streamRelayErrorDetails returns a loggable error string for unexpected stream
// failures. Expected client disconnects return nil so they do not pollute logs
// or request records.
func streamRelayErrorDetails(err error) *string {
	if err == nil || isExpectedStreamClose(err) {
		return nil
	}

	s := fmt.Sprintf("stream relay error: %v", err)
	return &s
}

func isExpectedStreamClose(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "context canceled")
}

package proxy

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamRelayErrorDetailsSuppressesExpectedDisconnects(t *testing.T) {
	t.Parallel()
	cases := []error{
		context.Canceled,
		io.ErrClosedPipe,
		errors.New("write tcp 127.0.0.1:4000->127.0.0.1:50000: write: broken pipe"),
		errors.New("flush stream: connection reset by peer"),
		errors.New("scanner failed: context canceled"),
	}

	for _, err := range cases {
		require.Nil(t, streamRelayErrorDetails(err))
		require.True(t, isExpectedStreamClose(err))
	}
}

func TestStreamRelayErrorDetailsReturnsUnexpectedFailures(t *testing.T) {
	t.Parallel()
	err := errors.New("unexpected EOF")

	details := streamRelayErrorDetails(err)
	require.NotNil(t, details)
	require.Equal(t, "stream relay error: unexpected EOF", *details)
	require.False(t, isExpectedStreamClose(err))
}

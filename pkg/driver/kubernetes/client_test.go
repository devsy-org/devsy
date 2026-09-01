package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForStream_ReturnsStreamError(t *testing.T) {
	wantErr := errors.New("boom")

	err := waitForStream(context.Background(), func(_ context.Context) error {
		return wantErr
	})

	require.ErrorIs(t, err, wantErr)
}

func TestWaitForStream_ReturnsNilOnSuccess(t *testing.T) {
	err := waitForStream(context.Background(), func(_ context.Context) error {
		return nil
	})

	assert.NoError(t, err)
}

// TestWaitForStream_CancellationIsNeverReportedAsSuccess is a regression test:
// a deliberately cancelled attempt (e.g. our own attempt deadline firing on a
// stalled exec stream) must be reported as a failure, not silently treated as
// a successful delivery.
func TestWaitForStream_CancellationIsNeverReportedAsSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	streamReturned := make(chan struct{})
	err := waitForStream(ctx, func(streamCtx context.Context) error {
		<-streamCtx.Done()
		close(streamReturned)
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	<-streamReturned
}

// TestWaitForStream_NeverReportsSuccessWhenContextAlreadyCancelled is a
// regression test for a race in the original select: ctx.Done() and errChan
// can both be ready at once, and selecting the errChan case with a nil
// stream error must still surface the cancellation rather than success.
func TestWaitForStream_NeverReportsSuccessWhenContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 200 {
		err := waitForStream(ctx, func(_ context.Context) error {
			return nil
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestWaitForStream_PropagatesRealStreamErrorEvenAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	wantErr := errors.New("stream reported a real error while unwinding")

	err := waitForStream(ctx, func(streamCtx context.Context) error {
		<-streamCtx.Done()
		return wantErr
	})

	require.ErrorIs(t, err, wantErr)
}

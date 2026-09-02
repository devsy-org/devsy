package tunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPersistentTransportStartsUnknown(t *testing.T) {
	t.Parallel()

	transport := NewPersistentTransport(context.Background())
	info := transport.CloseInfo()
	if info.Reason != CloseUnknown || info.Side != TransportSideUnknown || info.Err != nil {
		t.Fatalf("initial CloseInfo() = %+v, want unknown", info)
	}
}

func TestPersistentTransportFirstCloseWins(t *testing.T) {
	t.Parallel()

	transport := NewPersistentTransport(context.Background())
	firstErr := errors.New("provider stopped")
	transport.Close(TransportCloseInfo{
		Reason: CloseProviderError,
		Side:   TransportSideProvider,
		Err:    firstErr,
	})
	transport.Close(TransportCloseInfo{
		Reason: CloseSSHTransportError,
		Side:   TransportSideSSH,
		Err:    errors.New("ssh stopped"),
	})

	info := transport.CloseInfo()
	if info.Reason != CloseProviderError ||
		info.Side != TransportSideProvider ||
		!errors.Is(info.Err, firstErr) {
		t.Fatalf("CloseInfo() = %+v, want first close", info)
	}
	select {
	case <-transport.Done():
	default:
		t.Fatal("Done() is still open after Close")
	}
	select {
	case <-transport.Context().Done():
	default:
		t.Fatal("transport context is still active after Close")
	}
}

func TestPersistentTransportCloseIsSafeConcurrently(t *testing.T) {
	t.Parallel()

	transport := NewPersistentTransport(context.Background())
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			transport.Close(TransportCloseInfo{Reason: CloseLocalShutdown, Side: TransportSideSSH})
		})
	}
	wg.Wait()
	select {
	case <-transport.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() did not close")
	}
}

func TestPersistentTransportParentCancellationPropagates(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	transport := NewPersistentTransport(parent)
	cancel()

	select {
	case <-transport.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("transport context did not inherit parent cancellation")
	}
	if !errors.Is(transport.Context().Err(), context.Canceled) {
		t.Fatalf("transport context error = %v, want context.Canceled", transport.Context().Err())
	}
}

func TestClassifyTransportError(t *testing.T) {
	t.Parallel()

	for _, tt := range transportErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			parentCtx := context.Background()
			if tt.cancelParent {
				var cancel context.CancelFunc
				parentCtx, cancel = context.WithCancel(parentCtx)
				cancel()
			}
			got := classifyTransportError(tt.side, tt.err, parentCtx)
			if got.Reason != tt.wantReason || got.Side != tt.wantSide {
				t.Fatalf(
					"classifyTransportError() = %+v, want reason=%s side=%s",
					got,
					tt.wantReason,
					tt.wantSide,
				)
			}
			if tt.wantErr != nil && !errors.Is(got.Err, tt.wantErr) {
				t.Fatalf("classified error = %v, want %v", got.Err, tt.wantErr)
			}
		})
	}
}

type fmtWrappedEOF struct{}

func (fmtWrappedEOF) Error() string { return "peer: EOF" }
func (fmtWrappedEOF) Unwrap() error { return io.EOF }

type transportErrorTest struct {
	name         string
	side         TransportSide
	err          error
	cancelParent bool
	wantReason   TransportCloseReason
	wantSide     TransportSide
	wantErr      error
}

var (
	errProviderExit     = errors.New("provider exited")
	errSSHTransport     = errors.New("ssh failed")
	transportErrorTests = []transportErrorTest{
		{
			name:       "nil provider completion",
			side:       TransportSideProvider,
			wantReason: CloseLocalShutdown,
			wantSide:   TransportSideProvider,
		},
		{
			name:       "peer eof",
			side:       TransportSideSSH,
			err:        fmtWrappedEOF{},
			wantReason: ClosePeerEOF,
			wantSide:   TransportSideSSH,
		},
		{
			name:         "parent cancellation",
			side:         TransportSideProvider,
			err:          errProviderExit,
			cancelParent: true,
			wantReason:   CloseContextCancelled,
			wantSide:     TransportSideParent,
			wantErr:      context.Canceled,
		},
		{
			name:       "provider error",
			side:       TransportSideProvider,
			err:        errProviderExit,
			wantReason: CloseProviderError,
			wantSide:   TransportSideProvider,
			wantErr:    errProviderExit,
		},
		{
			name:       "ssh error",
			side:       TransportSideSSH,
			err:        errSSHTransport,
			wantReason: CloseSSHTransportError,
			wantSide:   TransportSideSSH,
			wantErr:    errSSHTransport,
		},
		{
			name:       "unknown side",
			side:       TransportSideUnknown,
			err:        errSSHTransport,
			wantReason: CloseUnknown,
			wantSide:   TransportSideUnknown,
			wantErr:    errSSHTransport,
		},
	}
)

func TestRunPersistentPairAttributesProviderFailure(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Close()
	providerErr := errors.New("provider failed")

	info, _ := runPersistentPair(
		context.Background(),
		pb,
		func(context.Context, *os.File, *os.File) error { return providerErr },
		func(ctx context.Context, _ *os.File, _ *os.File) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if info.Reason != CloseProviderError ||
		info.Side != TransportSideProvider ||
		!errors.Is(info.Err, providerErr) {
		t.Fatalf("close info = %+v, want provider failure", info)
	}
}

func TestRunPersistentPairAttributesSSHFailure(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Close()
	sshErr := errors.New("ssh failed")

	info, _ := runPersistentPair(
		context.Background(),
		pb,
		func(ctx context.Context, _ *os.File, _ *os.File) error {
			<-ctx.Done()
			return ctx.Err()
		},
		func(context.Context, *os.File, *os.File) error { return sshErr },
	)
	if info.Reason != CloseSSHTransportError ||
		info.Side != TransportSideSSH ||
		!errors.Is(info.Err, sshErr) {
		t.Fatalf("close info = %+v, want ssh failure", info)
	}
}

func TestRunPersistentPairAttributesParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Close()

	done := make(chan TransportCloseInfo, 1)
	go func() {
		info, _ := runPersistentPair(
			ctx,
			pb,
			func(ctx context.Context, _ *os.File, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
			func(ctx context.Context, _ *os.File, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
		)
		done <- info
	}()
	cancel()

	select {
	case info := <-done:
		if info.Reason != CloseContextCancelled || info.Side != TransportSideParent {
			t.Fatalf("close info = %+v, want parent cancellation", info)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("persistent pair did not stop after parent cancellation")
	}
}

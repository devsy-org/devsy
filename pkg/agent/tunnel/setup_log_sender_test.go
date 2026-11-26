package tunnel_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const testHook = "postCreateCommand"

// peer is one host: a store, a sink and an in-process gRPC server in front of
// the real tunnel server.
type peer struct {
	root   string
	store  *workspacelogs.Store
	acks   *workspacelogs.AckStore
	sink   *workspacelogs.Sink
	client tunnel.TunnelClient
	runID  string
	gate   chan struct{}
	calls  atomic.Int32
}

// gatedServer lets tests stall or hide the RPC in front of the real server.
type gatedServer struct {
	tunnel.TunnelServer
	p      *peer
	unimpl bool
}

func (g *gatedServer) AppendSetupLogs(
	ctx context.Context, req *tunnel.AppendSetupLogsRequest,
) (*tunnel.AppendSetupLogsResponse, error) {
	g.p.calls.Add(1)
	if g.unimpl {
		return nil, status.Error(codes.Unimplemented, "old peer")
	}
	if g.p.gate != nil {
		select {
		case <-g.p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return g.TunnelServer.AppendSetupLogs(ctx, req)
}

type peerOptions struct {
	runID      string
	unimpl     bool
	stalled    bool
	onAccepted func(string, uint64)
}

func newPeer(t *testing.T, o peerOptions) *peer {
	t.Helper()
	root := t.TempDir()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: root})
	require.NoError(t, err)
	acks, err := workspacelogs.NewAckStore(filepath.Join(t.TempDir(), "acks"))
	require.NoError(t, err)
	p := &peer{root: root, store: store, acks: acks, runID: o.runID}
	if p.runID == "" {
		p.runID = workspacelogs.NewID()
	}
	if o.stalled {
		p.gate = make(chan struct{})
	}
	p.sink, err = workspacelogs.NewSink(workspacelogs.SinkOptions{
		Store: store, Acks: acks, OnAccepted: o.onAccepted,
		Spec: workspacelogs.RunSpec{RunID: p.runID, Context: "default", Command: "up"},
	})
	require.NoError(t, err)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	real := tunnelserver.New(tunnelserver.WithSetupLogSink(p.sink))
	tunnel.RegisterTunnelServer(srv, &gatedServer{TunnelServer: real, p: p, unimpl: o.unimpl})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(
			func(context.Context, string) (net.Conn, error) { return lis.Dial() },
		),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	p.client = tunnel.NewTunnelClient(conn)

	return p
}

func (p *peer) release() { close(p.gate) }

func (p *peer) output(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	after := ""
	for {
		resp, err := p.store.Read(workspacelogs.ReadRequest{RunID: p.runID, After: after})
		require.NoError(t, err)
		for i := range resp.Records {
			b.WriteString(resp.Records[i].Text)
		}
		if !resp.HasMore {
			return b.String()
		}
		after = resp.NextCursor
	}
}

// source is the execution host: a store written through a Recorder.
type source struct {
	store *workspacelogs.Store
	rec   *workspacelogs.Recorder
	acks  *workspacelogs.AckStore
	inv   string
}

func newSource(t *testing.T, runID string) *source {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	_, err = store.CreateRun(workspacelogs.RunSpec{RunID: runID, Context: "default", Command: "up"})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: runID, Location: workspacelogs.LocationContainer,
	})
	require.NoError(t, err)
	acks, err := workspacelogs.NewAckStore(filepath.Join(t.TempDir(), "acks"))
	require.NoError(t, err)

	return &source{store: store, rec: rec, acks: acks, inv: workspacelogs.NewID()}
}

func (s *source) write(t *testing.T, lines ...string) {
	t.Helper()
	w := s.rec.Writer(s.inv, workspacelogs.StreamStdout)
	for _, l := range lines {
		_, err := w.Write([]byte(l))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
}

func (s *source) sender(
	ctx context.Context,
	client tunnel.TunnelClient,
	runID string,
) *tunnel.SetupLogSender {
	return tunnel.NewSetupLogSender(ctx, tunnel.SetupLogSenderOptions{
		Client:         client,
		Source:         workspacelogs.NewStoreSource(s.store),
		RunID:          runID,
		ProducerID:     s.rec.ProducerID(),
		Acks:           s.acks,
		CleanupTimeout: 2 * time.Second,
		CallTimeout:    time.Second,
		PollInterval:   20 * time.Millisecond,
	})
}

func TestSenderMirrorsAndResumesFromAcknowledgedPosition(t *testing.T) {
	p := newPeer(t, peerOptions{})
	src := newSource(t, p.runID)
	src.rec.Invocation(workspacelogs.Invocation{
		ID: src.inv, Phase: "setup", Hook: testHook, State: workspacelogs.InvocationRunning,
	})
	src.write(t, "one\n", "two\n")

	s := src.sender(context.Background(), p.client, p.runID)
	require.True(t, s.Flush(context.Background()))
	s.Close()
	require.Equal(t, "one\ntwo\n", p.output(t))
	callsBefore := p.calls.Load()

	// A new sender over the same acknowledgement store replays nothing.
	src.write(t, "three\n")
	again := src.sender(context.Background(), p.client, p.runID)
	defer again.Close()
	require.True(t, again.Flush(context.Background()))
	require.Equal(t, "one\ntwo\nthree\n", p.output(t))
	require.Equal(t, callsBefore+1, p.calls.Load())

	// Replaying from zero is a duplicate delivery and changes nothing.
	fresh, err := workspacelogs.NewAckStore(filepath.Join(t.TempDir(), "fresh"))
	require.NoError(t, err)
	src.acks = fresh
	replay := src.sender(context.Background(), p.client, p.runID)
	defer replay.Close()
	require.True(t, replay.Flush(context.Background()))
	require.Equal(t, "one\ntwo\nthree\n", p.output(t))
}

func TestSenderNeverBlocksProducerOnStalledPeer(t *testing.T) {
	p := newPeer(t, peerOptions{stalled: true})
	src := newSource(t, p.runID)
	s := src.sender(context.Background(), p.client, p.runID)
	defer s.Close()

	start := time.Now()
	for range 500 {
		src.write(t, "line\n")
		s.Notify()
	}
	require.Less(t, time.Since(start), 5*time.Second)

	flushStart := time.Now()
	require.False(t, s.Flush(context.Background()))
	require.Less(t, time.Since(flushStart), 4*time.Second)
	require.Empty(t, p.output(t))

	p.release()
	require.True(t, s.Flush(context.Background()))
	require.Equal(t, strings.Repeat("line\n", 500), p.output(t))
}

func TestSenderFallsBackWhenPeerIsOld(t *testing.T) {
	p := newPeer(t, peerOptions{unimpl: true})
	src := newSource(t, p.runID)
	src.write(t, "kept\n")
	s := src.sender(context.Background(), p.client, p.runID)
	defer s.Close()

	require.False(t, s.Flush(context.Background()))
	require.True(t, s.Disabled())
	require.Zero(t, s.Acked())
	require.Equal(t, "kept\n", sourceText(t, src, p.runID))
}

func TestSenderFinalDrainAfterCanceledContext(t *testing.T) {
	p := newPeer(t, peerOptions{})
	src := newSource(t, p.runID)
	src.write(t, "last words\n")
	ctx, cancel := context.WithCancel(context.Background())
	s := src.sender(ctx, p.client, p.runID)
	cancel()
	s.Close()

	require.True(t, s.Flush(ctx))
	require.Equal(t, "last words\n", p.output(t))
}

func TestSenderStopsRetryingExcludedBytes(t *testing.T) {
	p := newPeer(t, peerOptions{})
	src := newSource(t, p.runID)
	src.write(t, "a\n")
	s := src.sender(context.Background(), p.client, p.runID)
	defer s.Close()
	require.True(t, s.Flush(context.Background()))

	// Fill the run so the receiver store cannot take more bytes.
	junk := filepath.Join(p.root, "runs", p.runID, "junk")
	require.NoError(t, os.WriteFile(junk, make([]byte, workspacelogs.MaxRunBytes), 0o600))
	src.write(t, "b\n", "c\n")
	require.False(t, s.Flush(context.Background()))
	acked := s.Acked()
	require.Greater(t, acked, uint64(2))

	calls := p.calls.Load()
	require.False(t, s.Flush(context.Background()))
	require.Equal(t, calls, p.calls.Load())
	m, err := p.store.Manifest(p.runID)
	require.NoError(t, err)
	require.Contains(t, m.Conditions, workspacelogs.ConditionRetentionGap)
}

func TestTwoHopAcknowledgementsAreIndependent(t *testing.T) {
	runID := workspacelogs.NewID()
	caller := newPeer(t, peerOptions{runID: runID, stalled: true})

	var fwd *tunnel.SetupLogForwarder
	provider := newPeer(t, peerOptions{
		runID:      runID,
		onAccepted: func(id string, n uint64) { fwd.Notify(id, n) },
	})
	fwd = tunnel.NewSetupLogForwarder(context.Background(), tunnel.SetupLogForwarderOptions{
		Client:         caller.client,
		Source:         workspacelogs.NewStoreSource(provider.store),
		RunID:          runID,
		Acks:           provider.acks,
		CleanupTimeout: 600 * time.Millisecond,
		CallTimeout:    time.Second,
		PollInterval:   20 * time.Millisecond,
	})
	defer fwd.Close()

	src := newSource(t, runID)
	src.write(t, "from the container\n")
	s := src.sender(context.Background(), provider.client, runID)
	defer s.Close()

	// The provider spool accepts, so the container hop completes.
	require.True(t, s.Flush(context.Background()))
	require.Equal(t, "from the container\n", provider.output(t))
	// The caller has nothing yet, so the provider must not claim it.
	require.False(t, fwd.Flush(context.Background()))
	require.Empty(t, caller.output(t))
	up, err := provider.acks.Load(workspacelogs.AckKey{
		RunID: runID, ProducerID: src.rec.ProducerID(), Hop: workspacelogs.HopUpstream,
	})
	require.NoError(t, err)
	in, err := provider.acks.Load(workspacelogs.AckKey{
		RunID: runID, ProducerID: src.rec.ProducerID(), Hop: workspacelogs.HopInbound,
	})
	require.NoError(t, err)
	require.Zero(t, up.Acked)
	require.Positive(t, in.Acked)

	caller.release()
	require.True(t, fwd.Flush(context.Background()))
	require.Equal(t, "from the container\n", caller.output(t))
	// Identity is unchanged across hops.
	resp, err := caller.store.Read(workspacelogs.ReadRequest{RunID: runID})
	require.NoError(t, err)
	require.Equal(t, src.rec.ProducerID(), resp.Records[0].ProducerID)
	up, err = provider.acks.Load(workspacelogs.AckKey{
		RunID: runID, ProducerID: src.rec.ProducerID(), Hop: workspacelogs.HopUpstream,
	})
	require.NoError(t, err)
	require.Equal(t, in.Acked, up.Acked)
}

func sourceText(t *testing.T, src *source, runID string) string {
	t.Helper()
	resp, err := src.store.Read(workspacelogs.ReadRequest{RunID: runID})
	require.NoError(t, err)
	var b strings.Builder
	for i := range resp.Records {
		b.WriteString(resp.Records[i].Text)
	}

	return b.String()
}

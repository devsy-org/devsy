package tunnelserver

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type terminalSuite struct {
	suite.Suite
	req provider2.LifecycleTerminalRequest
}
type testTerminalOwner struct {
	serve func(context.Context, tunnel.Tunnel_LifecycleTerminalServer) error
}

func (o testTerminalOwner) Serve(
	ctx context.Context,
	stream tunnel.Tunnel_LifecycleTerminalServer,
) error {
	return o.serve(ctx, stream)
}
func TestLifecycleTerminalSuite(t *testing.T) { suite.Run(t, new(terminalSuite)) }
func (s *terminalSuite) SetupTest() {
	s.req = provider2.LifecycleTerminalRequest{
		ProtocolVersion: 1,
		WorkspaceUID:    "uid",
		Context:         "ctx",
		Nonce:           "nonce",
	}
}

func (s *terminalSuite) client(opts ...Option) tunnel.TunnelClient {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	tunnel.RegisterTunnelServer(server, New(opts...))
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient(
		"passthrough:///terminal",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(
			func(context.Context, string) (net.Conn, error) { return listener.Dial() },
		),
	)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return tunnel.NewTunnelClient(conn)
}

func ready(stream tunnel.Tunnel_LifecycleTerminalServer) error {
	return stream.Send(
		&tunnel.LifecycleTerminalResponse{
			Frame: &tunnel.LifecycleTerminalResponse_Ready{
				Ready: &tunnel.LifecycleTerminalReady{
					ProtocolVersion: 1,
					Term:            "xterm",
					Rows:            24,
					Columns:         80,
					Input:           true,
				},
			},
		},
	)
}

func ack(stream tunnel.Tunnel_LifecycleTerminalServer, id string) error {
	return stream.Send(
		&tunnel.LifecycleTerminalResponse{
			Frame: &tunnel.LifecycleTerminalResponse_BeginAck{
				BeginAck: &tunnel.LifecycleTerminalBeginAck{
					InvocationId: id,
					Rows:         25,
					Columns:      81,
				},
			},
		},
	)
}

func openFrame(req provider2.LifecycleTerminalRequest) *tunnel.LifecycleTerminalRequest {
	return &tunnel.LifecycleTerminalRequest{
		Frame: &tunnel.LifecycleTerminalRequest_Open{
			Open: &tunnel.LifecycleTerminalOpen{
				ProtocolVersion: req.ProtocolVersion,
				WorkspaceUid:    req.WorkspaceUID,
				Context:         req.Context,
				Nonce:           req.Nonce,
			},
		},
	}
}

func (s *terminalSuite) TestIdentityAndMissingOwner() {
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error { return ready(st) },
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	for _, field := range []string{"nonce", "uid", "context"} {
		s.Run(field, func() {
			req := s.req
			switch field {
			case "nonce":
				req.Nonce = "bad"
			case "uid":
				req.WorkspaceUID = "bad"
			case "context":
				req.Context = "bad"
			}
			st, err := client.LifecycleTerminal(context.Background())
			s.Require().NoError(err)
			s.Require().NoError(st.Send(openFrame(req)))
			_, err = st.Recv()
			s.Equal(codes.PermissionDenied, status.Code(err))
		})
	}
	st, err := s.client().LifecycleTerminal(context.Background())
	s.Require().NoError(err)
	_ = st.Send(openFrame(s.req))
	_, err = st.Recv()
	s.Equal(codes.FailedPrecondition, status.Code(err))
}

func (s *terminalSuite) TestDuplicateOwner() {
	release := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(ctx context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	first, err := client.LifecycleTerminal(context.Background())
	s.Require().NoError(err)
	s.Require().NoError(first.Send(openFrame(s.req)))
	_, err = first.Recv()
	s.Require().NoError(err)
	second, err := client.LifecycleTerminal(context.Background())
	s.Require().NoError(err)
	s.Require().NoError(second.Send(openFrame(s.req)))
	_, err = second.Recv()
	s.Equal(codes.FailedPrecondition, status.Code(err))
	close(release)
}

func (s *terminalSuite) TestSessionOutputAndUseAfterExit() {
	received := make(chan []byte, 1)
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			if err = ack(st, f.GetBegin().InvocationId); err != nil {
				return err
			}
			var data []byte
			for {
				f, err = st.Recv()
				if err != nil {
					return err
				}
				if out := f.GetOutput(); out != nil {
					data = append(data, out.Data...)
					continue
				}
				if ex := f.GetExit(); ex != nil {
					received <- data
					if err = st.Send(
						&tunnel.LifecycleTerminalResponse{
							Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
								ExitAck: &tunnel.LifecycleTerminalExitAck{
									InvocationId: ex.InvocationId,
								},
							},
						},
					); err != nil {
						return err
					}
					break
				}
			}
			_, err = st.Recv()
			return err
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "one", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	s.Equal(uint16(25), a.Window().Rows)
	_, err = session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "two", Phase: "postStartCommand"},
	)
	s.Equal(codes.FailedPrecondition, status.Code(err))
	data := bytes.Repeat([]byte("abcdef"), 100000)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { ; _, _ = a.Output().Write(data) })
	}
	wg.Wait()
	s.Require().NoError(a.Exit(context.Background(), tunnel.TerminalExit{ExitCode: 3}))
	s.Equal(append(bytes.Clone(data), data...), <-received)
	_, err = a.Output().Write([]byte("x"))
	s.Equal(codes.FailedPrecondition, status.Code(err))
	s.Require().NoError(session.Close(context.Background()))
	s.NoError(session.Close(context.Background()))
}

func (s *terminalSuite) TestInputOverflow() {
	proceed := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			<-proceed
			for range 9 {
				if err = st.Send(
					&tunnel.LifecycleTerminalResponse{
						Frame: &tunnel.LifecycleTerminalResponse_Input{
							Input: &tunnel.LifecycleTerminalInput{
								InvocationId: id,
								Data:         make([]byte, 32<<10),
							},
						},
					},
				); err != nil {
					return err
				}
			}
			<-st.Context().Done()
			return st.Context().Err()
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	close(proceed)
	select {
	case <-a.Canceled():
	case <-time.After(5 * time.Second):
		s.FailNow("input overflow did not cancel")
	}
	err = a.Exit(context.Background(), tunnel.TerminalExit{})
	s.Error(err)
	s.Eventually(func() bool {
		var buf [32768]byte
		_, e := a.Input().Read(buf[:])
		return status.Code(e) == codes.ResourceExhausted
	}, time.Second, time.Millisecond)
}

var _ io.Reader = (*terminalInput)(nil)

func (s *terminalSuite) TestInvalidRequestFrames() {
	tests := []struct {
		name  string
		frame *tunnel.LifecycleTerminalRequest
		code  codes.Code
	}{
		{
			"out_of_order",
			&tunnel.LifecycleTerminalRequest{
				Frame: &tunnel.LifecycleTerminalRequest_Output{
					Output: &tunnel.LifecycleTerminalOutput{InvocationId: "id", Sequence: 2},
				},
			},
			codes.FailedPrecondition,
		},
		{
			"wrong_invocation",
			&tunnel.LifecycleTerminalRequest{
				Frame: &tunnel.LifecycleTerminalRequest_Output{
					Output: &tunnel.LifecycleTerminalOutput{InvocationId: "other", Sequence: 1},
				},
			},
			codes.FailedPrecondition,
		},
		{
			"oversized",
			&tunnel.LifecycleTerminalRequest{
				Frame: &tunnel.LifecycleTerminalRequest_Output{
					Output: &tunnel.LifecycleTerminalOutput{
						InvocationId: "id",
						Sequence:     1,
						Data:         make([]byte, (32<<10)+1),
					},
				},
			},
			codes.ResourceExhausted,
		},
		{
			"second_begin",
			&tunnel.LifecycleTerminalRequest{
				Frame: &tunnel.LifecycleTerminalRequest_Begin{
					Begin: &tunnel.LifecycleTerminalBegin{
						InvocationId: "two",
						Phase:        "postStartCommand",
					},
				},
			},
			codes.FailedPrecondition,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			owner := testTerminalOwner{
				serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
					if err := ready(st); err != nil {
						return err
					}
					f, err := st.Recv()
					if err != nil {
						return err
					}
					if err = ack(st, f.GetBegin().InvocationId); err != nil {
						return err
					}
					_, err = st.Recv()
					return err
				},
			}
			client := s.client(WithLifecycleTerminalOwner(owner, s.req))
			st, err := client.LifecycleTerminal(context.Background())
			s.Require().NoError(err)
			s.Require().NoError(st.Send(openFrame(s.req)))
			_, err = st.Recv()
			s.Require().NoError(err)
			s.Require().
				NoError(st.Send(&tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Begin{Begin: &tunnel.LifecycleTerminalBegin{InvocationId: "id", Phase: "postStartCommand"}}}))
			_, err = st.Recv()
			s.Require().NoError(err)
			s.Require().NoError(st.Send(tc.frame))
			_, err = st.Recv()
			s.Equal(tc.code, status.Code(err))
		})
	}
}

func (s *terminalSuite) TestTwoHopAndOtherRPCsWhileStalled() {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(ctx context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			defer close(finished)
			if err := ready(st); err != nil {
				return err
			}
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	cli := s.client(WithLifecycleTerminalOwner(owner, s.req))
	host := s.client(
		WithLifecycleTerminalUpstream(cli, s.req),
		WithAllowGitCredentials(true),
		WithGitToken(
			&provider2.GitToken{Host: "example.com", Username: "user", Token: "test-token"},
		),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := OpenLifecycleTerminal(ctx, host, s.req)
	s.Require().NoError(err)
	<-started
	rpcCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	_, err = host.Status(rpcCtx, &tunnel.StatusUpdate{Phase: "test", State: "started"})
	s.NoError(err)
	_, err = host.SendResult(rpcCtx, &tunnel.Message{Message: "{}"})
	s.NoError(err)
	_, err = host.GitCredentials(rpcCtx, &tunnel.Message{Message: `{"host":"example.com"}`})
	s.NoError(err)
	cancel()
	close(release)
	s.Eventually(func() bool {
		select {
		case <-finished:
			return true
		default:
			return false
		}
	}, 3*time.Second, 10*time.Millisecond)
	_ = session.Close(context.Background())
}

func (s *terminalSuite) TestExitAckTimeoutClosesSession() {
	owner := testTerminalOwner{
		serve: func(ctx context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			if err = ack(st, f.GetBegin().InvocationId); err != nil {
				return err
			}
			_, err = st.Recv()
			if err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	started := time.Now()
	err = a.Exit(context.Background(), tunnel.TerminalExit{ExitCode: 17})
	s.GreaterOrEqual(time.Since(started), tunnel.LifecycleTerminalExitAckTimeout)
	s.ErrorIs(err, context.DeadlineExceeded)
	_, err = session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "next", Phase: "postStartCommand"},
	)
	s.Error(err)
}

func (s *terminalSuite) TestTwoHopFrameExchange() {
	data := []byte("prompt without newline")
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			f, err = st.Recv()
			if err != nil {
				return err
			}
			if !bytes.Equal(f.GetOutput().Data, data) {
				return status.Error(codes.DataLoss, "wrong data")
			}
			if err = st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_Input{
						Input: &tunnel.LifecycleTerminalInput{
							InvocationId: id,
							Data:         []byte("reply"),
						},
					},
				},
			); err != nil {
				return err
			}
			f, err = st.Recv()
			if err != nil {
				return err
			}
			if f.GetExit() == nil {
				return status.Error(codes.FailedPrecondition, "missing exit")
			}
			if err = st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
						ExitAck: &tunnel.LifecycleTerminalExitAck{InvocationId: id},
					},
				},
			); err != nil {
				return err
			}
			_, err = st.Recv()
			return err
		},
	}
	cli := s.client(WithLifecycleTerminalOwner(owner, s.req))
	host := s.client(WithLifecycleTerminalUpstream(cli, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), host, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	_, err = a.Output().Write(data)
	s.Require().NoError(err)
	buf := make([]byte, 5)
	_, err = io.ReadFull(a.Input(), buf)
	s.Require().NoError(err)
	s.Equal("reply", string(buf))
	s.NoError(a.Exit(context.Background(), tunnel.TerminalExit{}))
	s.NoError(session.Close(context.Background()))
}

func (s *terminalSuite) TestConcurrentOwnerSendsSerialize() {
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			errs := make(chan error, 2)
			for _, b := range []byte{'a', 'b'} {
				go func(c byte) {
					errs <- st.Send(&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_Input{Input: &tunnel.LifecycleTerminalInput{InvocationId: id, Data: []byte{c}}}})
				}(b)
			}
			for range 2 {
				if err = <-errs; err != nil {
					return err
				}
			}
			f, err = st.Recv()
			if err != nil {
				return err
			}
			if err = st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
						ExitAck: &tunnel.LifecycleTerminalExitAck{
							InvocationId: f.GetExit().InvocationId,
						},
					},
				},
			); err != nil {
				return err
			}
			_, err = st.Recv()
			return err
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	buf := make([]byte, 2)
	_, err = io.ReadFull(a.Input(), buf)
	s.Require().NoError(err)
	s.ElementsMatch([]byte("ab"), buf)
	s.NoError(a.Exit(context.Background(), tunnel.TerminalExit{}))
	s.NoError(session.Close(context.Background()))
}

func (s *terminalSuite) TestGoroutinesExitAfterClose() {
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			_, err := st.Recv()
			return err
		},
	}
	cli := s.client(WithLifecycleTerminalOwner(owner, s.req))
	host := s.client(WithLifecycleTerminalUpstream(cli, s.req))
	_, err := cli.Ping(context.Background(), &tunnel.Empty{})
	s.Require().NoError(err)
	_, err = host.Ping(context.Background(), &tunnel.Empty{})
	s.Require().NoError(err)
	opts := []goleak.Option{goleak.IgnoreCurrent()}
	session, err := OpenLifecycleTerminal(context.Background(), host, s.req)
	s.Require().NoError(err)
	s.Require().NoError(session.Close(context.Background()))
	s.Eventually(
		func() bool { return goleak.Find(opts...) == nil },
		5*time.Second,
		50*time.Millisecond,
	)
	s.NoError(goleak.Find(opts...))
}

func (s *terminalSuite) TestInputCloseUnblocksRead() {
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			f, err = st.Recv()
			if err != nil {
				return err
			}
			if err = st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
						ExitAck: &tunnel.LifecycleTerminalExitAck{InvocationId: id},
					},
				},
			); err != nil {
				return err
			}
			_, err = st.Recv()
			return err
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	reader, ok := a.Input().(io.ReadCloser)
	s.Require().True(ok)
	done := make(chan error, 1)
	go func() { var b [1]byte; _, readErr := reader.Read(b[:]); done <- readErr }()
	s.Require().NoError(reader.Close())
	select {
	case err = <-done:
		s.ErrorIs(err, io.EOF)
	case <-time.After(time.Second):
		s.FailNow("read did not unblock")
	}
	s.NoError(a.Exit(context.Background(), tunnel.TerminalExit{}))
	s.NoError(session.Close(context.Background()))
}

func (s *terminalSuite) TestOutputBackpressurePreservesBytes() {
	received := make(chan []byte, 1)
	stalled := make(chan struct{})
	release := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(ctx context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			close(stalled)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			var data []byte
			for {
				f, err = st.Recv()
				if err != nil {
					return err
				}
				if out := f.GetOutput(); out != nil {
					data = append(data, out.Data...)
					continue
				}
				if f.GetExit() != nil {
					received <- data
					if err = st.Send(
						&tunnel.LifecycleTerminalResponse{
							Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
								ExitAck: &tunnel.LifecycleTerminalExitAck{InvocationId: id},
							},
						},
					); err != nil {
						return err
					}
					break
				}
			}
			_, err = st.Recv()
			return err
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	payload := bytes.Repeat([]byte("12345678"), 1<<20)
	done := make(chan error, 1)
	go func() { _, writeErr := a.Output().Write(payload); done <- writeErr }()
	<-stalled
	select {
	case err = <-done:
		s.FailNow("write did not apply backpressure", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err = <-done:
		s.NoError(err)
	case <-time.After(20 * time.Second):
		s.FailNow("write did not finish")
	}
	s.NoError(a.Exit(context.Background(), tunnel.TerminalExit{}))
	s.Equal(payload, <-received)
	s.NoError(session.Close(context.Background()))
}

func (s *terminalSuite) TestInvalidResponseFrames() {
	tests := []struct {
		name  string
		frame *tunnel.LifecycleTerminalResponse
		code  codes.Code
	}{
		{
			"wrong_begin_ack",
			&tunnel.LifecycleTerminalResponse{
				Frame: &tunnel.LifecycleTerminalResponse_BeginAck{
					BeginAck: &tunnel.LifecycleTerminalBeginAck{
						InvocationId: "wrong",
						Rows:         20,
						Columns:      80,
					},
				},
			},
			codes.FailedPrecondition,
		},
		{
			"input_before_ack",
			&tunnel.LifecycleTerminalResponse{
				Frame: &tunnel.LifecycleTerminalResponse_Input{
					Input: &tunnel.LifecycleTerminalInput{InvocationId: "id", Data: []byte("x")},
				},
			},
			codes.FailedPrecondition,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			owner := testTerminalOwner{
				serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
					if err := ready(st); err != nil {
						return err
					}
					if _, err := st.Recv(); err != nil {
						return err
					}
					return st.Send(tc.frame)
				},
			}
			client := s.client(WithLifecycleTerminalOwner(owner, s.req))
			session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
			s.Require().NoError(err)
			_, err = session.Begin(
				context.Background(),
				tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
			)
			s.Equal(tc.code, status.Code(err))
		})
	}
}

func (s *terminalSuite) TestCancelWithFullInputBuffer() {
	proceed := make(chan struct{})
	sent := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(ctx context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			<-proceed
			for range 8 {
				if err = st.Send(
					&tunnel.LifecycleTerminalResponse{
						Frame: &tunnel.LifecycleTerminalResponse_Input{
							Input: &tunnel.LifecycleTerminalInput{
								InvocationId: id,
								Data:         make([]byte, 32<<10),
							},
						},
					},
				); err != nil {
					return err
				}
			}
			if err = st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_Cancel{
						Cancel: &tunnel.LifecycleTerminalCancel{InvocationId: id},
					},
				},
			); err != nil {
				return err
			}
			close(sent)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	close(proceed)
	<-sent
	select {
	case <-a.Canceled():
	case <-time.After(3 * time.Second):
		s.FailNow("cancel did not arrive")
	}
	err = a.Exit(context.Background(), tunnel.TerminalExit{})
	s.Equal(codes.Canceled, status.Code(err))
}

func (s *terminalSuite) TestBlockedServerSendExitsAfterHandlerReturn() {
	release := make(chan struct{})
	senderDone := make(chan struct{})
	sending := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			go func() {
				defer close(senderDone)
				close(sending)
				for {
					err = st.Send(
						&tunnel.LifecycleTerminalResponse{
							Frame: &tunnel.LifecycleTerminalResponse_Input{
								Input: &tunnel.LifecycleTerminalInput{
									InvocationId: id,
									Data:         make([]byte, 32<<10),
								},
							},
						},
					)
					if err != nil {
						return
					}
				}
			}()
			<-release
			return nil
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	st, err := client.LifecycleTerminal(context.Background())
	s.Require().NoError(err)
	s.Require().NoError(st.Send(openFrame(s.req)))
	_, err = st.Recv()
	s.Require().NoError(err)
	s.Require().
		NoError(st.Send(&tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Begin{Begin: &tunnel.LifecycleTerminalBegin{InvocationId: "id", Phase: "postStartCommand"}}}))
	_, err = st.Recv()
	s.Require().NoError(err)
	<-sending
	time.Sleep(100 * time.Millisecond)
	close(release)
	s.Eventually(func() bool {
		select {
		case <-senderDone:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
}

func (s *terminalSuite) TestOversizedInputFrame() {
	proceed := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			f, err := st.Recv()
			if err != nil {
				return err
			}
			id := f.GetBegin().InvocationId
			if err = ack(st, id); err != nil {
				return err
			}
			<-proceed
			return st.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_Input{
						Input: &tunnel.LifecycleTerminalInput{
							InvocationId: id,
							Data:         make([]byte, (32<<10)+1),
						},
					},
				},
			)
		},
	}
	client := s.client(WithLifecycleTerminalOwner(owner, s.req))
	session, err := OpenLifecycleTerminal(context.Background(), client, s.req)
	s.Require().NoError(err)
	a, err := session.Begin(
		context.Background(),
		tunnel.TerminalInvocation{ID: "id", Phase: "postStartCommand"},
	)
	s.Require().NoError(err)
	close(proceed)
	select {
	case <-a.Canceled():
	case <-time.After(3 * time.Second):
		s.FailNow("oversized input did not fail")
	}
	err = a.Exit(context.Background(), tunnel.TerminalExit{})
	s.Equal(codes.ResourceExhausted, status.Code(err))
}

func (s *terminalSuite) TestForwarderReceiveExitsAfterUpstreamFailure() {
	release := make(chan struct{})
	owner := testTerminalOwner{
		serve: func(_ context.Context, st tunnel.Tunnel_LifecycleTerminalServer) error {
			if err := ready(st); err != nil {
				return err
			}
			<-release
			return status.Error(codes.Aborted, "upstream stopped")
		},
	}
	cli := s.client(WithLifecycleTerminalOwner(owner, s.req))
	host := s.client(WithLifecycleTerminalUpstream(cli, s.req))
	_, err := cli.Ping(context.Background(), &tunnel.Empty{})
	s.Require().NoError(err)
	_, err = host.Ping(context.Background(), &tunnel.Empty{})
	s.Require().NoError(err)
	opts := []goleak.Option{goleak.IgnoreCurrent()}
	session, err := OpenLifecycleTerminal(context.Background(), host, s.req)
	s.Require().NoError(err)
	close(release)
	s.Eventually(
		func() bool { return goleak.Find(opts...) == nil },
		5*time.Second,
		50*time.Millisecond,
	)
	s.NoError(goleak.Find(opts...))
	_ = session.Close(context.Background())
}

package tunnelserver

import (
	"context"
	"io"
	"sync"
	"unicode"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var terminalOwners sync.Map

func (t *tunnelServer) serveLifecycleTerminal(stream tunnel.Tunnel_LifecycleTerminalServer) error {
	if t.lifecycleTerminalOwner == nil && t.lifecycleTerminalUpstream == nil {
		return status.Error(codes.FailedPrecondition, "lifecycle terminal unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	req := t.lifecycleTerminalRequest
	if open == nil || open.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion ||
		open.ProtocolVersion != req.ProtocolVersion ||
		open.WorkspaceUid == "" ||
		open.WorkspaceUid != req.WorkspaceUID ||
		open.Context != req.Context ||
		!validIdentity(open.Context, false) ||
		!validIdentity(open.WorkspaceUid, true) ||
		!validIdentity(open.Nonce, true) ||
		open.Nonce == "" ||
		open.Nonce != req.Nonce {
		return status.Error(codes.PermissionDenied, "invalid lifecycle terminal identity")
	}
	if _, loaded := terminalOwners.LoadOrStore(t, struct{}{}); loaded {
		return status.Error(codes.FailedPrecondition, "lifecycle terminal already reserved")
	}
	defer terminalOwners.Delete(t)
	checked := &checkedTerminalStream{
		Tunnel_LifecycleTerminalServer: stream,
		sends:                          make(chan serverSend),
		stop:                           make(chan struct{}),
		stopped:                        make(chan struct{}),
	}
	go checked.sendLoop()
	defer checked.closeSender()
	if t.lifecycleTerminalOwner != nil {
		return t.lifecycleTerminalOwner.Serve(stream.Context(), checked)
	}
	stream = checked
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	upstream, err := t.lifecycleTerminalUpstream.LifecycleTerminal(ctx)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		if sendErr := upstream.Send(first); sendErr != nil {
			done <- sendErr
			cancel()
			return
		}
		for {
			frame, recvErr := stream.Recv()
			if recvErr == io.EOF {
				done <- upstream.CloseSend()
				return
			}
			if recvErr != nil {
				done <- recvErr
				cancel()
				return
			}
			if sendErr := upstream.Send(frame); sendErr != nil {
				done <- sendErr
				cancel()
				return
			}
			if frame.GetComplete() != nil {
				done <- upstream.CloseSend()
				return
			}
		}
	}()
	for {
		frame, recvErr := upstream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				return nil
			}
			select {
			case sendErr := <-done:
				if sendErr != nil {
					return sendErr
				}
			default:
			}
			return recvErr
		}
		if err = stream.Send(frame); err != nil {
			return err
		}
	}
}

type checkedTerminalStream struct {
	tunnel.Tunnel_LifecycleTerminalServer
	mu         sync.Mutex
	sends      chan serverSend
	stop       chan struct{}
	stopped    chan struct{}
	phase      terminalClientPhase
	invocation string
	sequence   uint64
	closed     bool
	sending    bool
}

func (s *checkedTerminalStream) Recv() (*tunnel.LifecycleTerminalRequest, error) {
	frame, err := s.Tunnel_LifecycleTerminalServer.Recv()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	invalid := func() (*tunnel.LifecycleTerminalRequest, error) {
		return nil, status.Error(codes.FailedPrecondition, "unexpected lifecycle terminal request")
	}
	if frame == nil {
		return invalid()
	}
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalRequest_Begin:
		v := f.Begin
		if s.phase != clientIdle || v == nil {
			return invalid()
		}
		if !validIdentity(v.InvocationId, true) || !validIdentity(v.Key, false) ||
			v.Phase != "postStartCommand" {
			return nil, status.Error(codes.InvalidArgument, "invalid lifecycle terminal invocation")
		}
		s.invocation = v.InvocationId
		s.sequence = 0
		s.phase = clientBeginAck
	case *tunnel.LifecycleTerminalRequest_Output:
		v := f.Output
		if s.phase != clientActive || v == nil || v.InvocationId != s.invocation ||
			v.Sequence != s.sequence+1 {
			return invalid()
		}
		if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
			return nil, status.Error(codes.ResourceExhausted, "terminal output frame too large")
		}
		s.sequence = v.Sequence
	case *tunnel.LifecycleTerminalRequest_Exit:
		v := f.Exit
		if s.phase != clientActive || v == nil || v.InvocationId != s.invocation {
			return invalid()
		}
		if !validIdentity(v.Signal, false) || !validIdentity(v.StartError, false) {
			return nil, status.Error(codes.InvalidArgument, "invalid lifecycle terminal exit")
		}
		s.phase = clientExitAck
	case *tunnel.LifecycleTerminalRequest_Complete:
		if s.phase != clientIdle || f.Complete == nil {
			return invalid()
		}
		s.phase = clientClosed
	default:
		return invalid()
	}
	return frame, nil
}

type serverSend struct {
	frame  *tunnel.LifecycleTerminalResponse
	result chan error
}

func (s *checkedTerminalStream) Send(frame *tunnel.LifecycleTerminalResponse) error {
	item := serverSend{frame: frame, result: make(chan error, 1)}
	select {
	case s.sends <- item:
	case <-s.stop:
		return context.Canceled
	case <-s.Context().Done():
		return s.Context().Err()
	}
	select {
	case err := <-item.result:
		return err
	case <-s.stop:
		return context.Canceled
	case <-s.Context().Done():
		return s.Context().Err()
	}
}

func (s *checkedTerminalStream) sendLoop() {
	defer close(s.stopped)
	for {
		select {
		case item := <-s.sends:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				item.result <- context.Canceled
				continue
			}
			err := s.validateResponse(item.frame)
			if err == nil {
				s.sending = true
			}
			s.mu.Unlock()
			if err == nil {
				err = s.Tunnel_LifecycleTerminalServer.Send(item.frame)
				s.mu.Lock()
				s.sending = false
				s.mu.Unlock()
			}
			item.result <- err
		case <-s.stop:
			return
		case <-s.Context().Done():
			return
		}
	}
}

func (s *checkedTerminalStream) closeSender() {
	s.mu.Lock()
	s.closed = true
	sending := s.sending
	close(s.stop)
	s.mu.Unlock()
	if !sending {
		<-s.stopped
	}
}

func (s *checkedTerminalStream) validateResponse(frame *tunnel.LifecycleTerminalResponse) error {
	invalid := func() error { return status.Error(codes.FailedPrecondition, "unexpected lifecycle terminal response") }
	if frame == nil {
		return invalid()
	}
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalResponse_Ready:
		v := f.Ready
		if s.phase != clientReady || v == nil {
			return invalid()
		}
		if v.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion || !v.Input ||
			!validSize(v.Rows, v.Columns) ||
			!validTerm(v.Term) {
			return status.Error(codes.InvalidArgument, "invalid terminal ready")
		}
		s.phase = clientIdle
	case *tunnel.LifecycleTerminalResponse_BeginAck:
		v := f.BeginAck
		if s.phase != clientBeginAck || v == nil || v.InvocationId != s.invocation {
			return invalid()
		}
		if !validSize(v.Rows, v.Columns) {
			return status.Error(codes.InvalidArgument, "invalid terminal size")
		}
		s.phase = clientActive
	case *tunnel.LifecycleTerminalResponse_Input:
		v := f.Input
		if (s.phase != clientActive && s.phase != clientExitAck) || v == nil ||
			v.InvocationId != s.invocation {
			return invalid()
		}
		if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
			return status.Error(codes.ResourceExhausted, "terminal input frame too large")
		}
	case *tunnel.LifecycleTerminalResponse_Resize:
		v := f.Resize
		if (s.phase != clientActive && s.phase != clientExitAck) || v == nil ||
			v.InvocationId != s.invocation {
			return invalid()
		}
		if !validSize(v.Rows, v.Columns) {
			return status.Error(codes.InvalidArgument, "invalid terminal size")
		}
	case *tunnel.LifecycleTerminalResponse_Cancel:
		if s.phase != clientActive || f.Cancel == nil || f.Cancel.InvocationId != s.invocation {
			return invalid()
		}
	case *tunnel.LifecycleTerminalResponse_ExitAck:
		if s.phase != clientExitAck || f.ExitAck == nil || f.ExitAck.InvocationId != s.invocation {
			return invalid()
		}
		s.phase = clientIdle
	default:
		return invalid()
	}
	return nil
}

func validIdentity(s string, required bool) bool {
	if len(s) > 256 || required && s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

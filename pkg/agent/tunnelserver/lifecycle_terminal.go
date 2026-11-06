package tunnelserver

import (
	"context"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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
	if !t.validTerminalOpen(first.GetOpen()) {
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
	return t.forwardLifecycleTerminal(checked, first)
}

func (t *tunnelServer) forwardLifecycleTerminal(
	checked *checkedTerminalStream,
	first *tunnel.LifecycleTerminalRequest,
) error {
	stream := tunnel.Tunnel_LifecycleTerminalServer(checked)
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	upstream, err := t.lifecycleTerminalUpstream.LifecycleTerminal(ctx)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go forwardTerminalRequests(stream, upstream, first, func(err error, cancelStream bool) {
		done <- err
		if cancelStream {
			cancel()
		}
	})
	return forwardTerminalResponses(stream, upstream, done)
}

func forwardTerminalResponses(
	stream tunnel.Tunnel_LifecycleTerminalServer,
	upstream tunnel.Tunnel_LifecycleTerminalClient,
	done <-chan error,
) error {
	var err error
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

func forwardTerminalRequests(
	stream tunnel.Tunnel_LifecycleTerminalServer,
	upstream tunnel.Tunnel_LifecycleTerminalClient,
	first *tunnel.LifecycleTerminalRequest,
	finish func(error, bool),
) {
	if sendErr := upstream.Send(first); sendErr != nil {
		finish(sendErr, true)
		return
	}
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			finish(upstream.CloseSend(), false)
			return
		}
		if recvErr != nil {
			finish(recvErr, true)
			return
		}
		if sendErr := upstream.Send(frame); sendErr != nil {
			finish(sendErr, true)
			return
		}
		if frame.GetComplete() != nil {
			finish(upstream.CloseSend(), false)
			return
		}
	}
}

func (t *tunnelServer) validTerminalOpen(open *tunnel.LifecycleTerminalOpen) bool {
	if open == nil {
		return false
	}
	req := t.lifecycleTerminalRequest
	if open.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion {
		return false
	}
	return open.ProtocolVersion == req.ProtocolVersion && t.validTerminalIdentity(open)
}

func (t *tunnelServer) validTerminalIdentity(open *tunnel.LifecycleTerminalOpen) bool {
	req := t.lifecycleTerminalRequest
	return open.WorkspaceUid != "" && open.WorkspaceUid == req.WorkspaceUID &&
		open.Context == req.Context && validIdentity(open.Context, false) &&
		validIdentity(open.WorkspaceUid, true) && validIdentity(open.Nonce, true) &&
		open.Nonce != "" && open.Nonce == req.Nonce
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

	if frame == nil {
		return nil, invalidTerminalRequest()
	}
	if err = s.dispatchRequest(frame); err != nil {
		return nil, err
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

func terminalDisplayKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if unicode.IsControl(r) {
			r = ' '
		}
		n := utf8.RuneLen(r)
		if n < 0 {
			r = utf8.RuneError
			n = utf8.RuneLen(r)
		}
		if b.Len()+n > 256 {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func invalidTerminalRequest() error {
	return status.Error(codes.FailedPrecondition, "unexpected lifecycle terminal request")
}

func invalidTerminalResponse() error {
	return status.Error(codes.FailedPrecondition, "unexpected lifecycle terminal response")
}

func (s *checkedTerminalStream) dispatchRequest(frame *tunnel.LifecycleTerminalRequest) error {
	var err error
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalRequest_Begin:
		err = s.receiveBegin(f)
	case *tunnel.LifecycleTerminalRequest_Output:
		err = s.receiveOutput(f)
	case *tunnel.LifecycleTerminalRequest_Exit:
		err = s.receiveExit(f)
	case *tunnel.LifecycleTerminalRequest_Complete:
		err = s.receiveComplete(f)
	default:
		err = invalidTerminalRequest()
	}
	if err != nil {
		return err
	}
	return nil
}

func (s *checkedTerminalStream) receiveBegin(f *tunnel.LifecycleTerminalRequest_Begin) error {
	v := f.Begin
	if s.phase != clientIdle || v == nil {
		return invalidTerminalRequest()
	}
	if !validIdentity(v.InvocationId, true) || !validIdentity(v.Key, false) ||
		v.Phase != "postStartCommand" {
		return status.Error(codes.InvalidArgument, "invalid lifecycle terminal invocation")
	}
	s.invocation = v.InvocationId
	s.sequence = 0
	s.phase = clientBeginAck
	return nil
}

func (s *checkedTerminalStream) receiveOutput(f *tunnel.LifecycleTerminalRequest_Output) error {
	v := f.Output
	if s.phase != clientActive || v == nil || v.InvocationId != s.invocation ||
		v.Sequence != s.sequence+1 {
		return invalidTerminalRequest()
	}
	if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
		return status.Error(codes.ResourceExhausted, "terminal output frame too large")
	}
	s.sequence = v.Sequence
	return nil
}

func (s *checkedTerminalStream) receiveExit(f *tunnel.LifecycleTerminalRequest_Exit) error {
	v := f.Exit
	if s.phase != clientActive || v == nil || v.InvocationId != s.invocation {
		return invalidTerminalRequest()
	}
	if !validIdentity(v.Signal, false) || !validIdentity(v.StartError, false) {
		return status.Error(codes.InvalidArgument, "invalid lifecycle terminal exit")
	}
	s.phase = clientExitAck
	return nil
}

func (s *checkedTerminalStream) receiveComplete(f *tunnel.LifecycleTerminalRequest_Complete) error {
	if s.phase != clientIdle || f.Complete == nil {
		return invalidTerminalRequest()
	}
	s.phase = clientClosed
	return nil
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
	if frame == nil {
		return invalidTerminalResponse()
	}
	return s.dispatchResponse(frame)
}

func (s *checkedTerminalStream) dispatchResponse(frame *tunnel.LifecycleTerminalResponse) error {
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalResponse_Ready:
		return s.validateReady(f)
	case *tunnel.LifecycleTerminalResponse_BeginAck:
		return s.validateBeginAck(f)
	case *tunnel.LifecycleTerminalResponse_Input:
		return s.validateInput(f)
	case *tunnel.LifecycleTerminalResponse_Resize:
		return s.validateResize(f)
	case *tunnel.LifecycleTerminalResponse_Cancel:
		return s.validateCancel(f)
	case *tunnel.LifecycleTerminalResponse_ExitAck:
		return s.validateExitAck(f)
	default:
		return invalidTerminalResponse()
	}
}

func (s *checkedTerminalStream) validateReady(f *tunnel.LifecycleTerminalResponse_Ready) error {
	v := f.Ready
	if s.phase != clientReady || v == nil {
		return invalidTerminalResponse()
	}
	if v.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion || !v.Input ||
		!validSize(v.Rows, v.Columns) ||
		!validTerm(v.Term) {
		return status.Error(codes.InvalidArgument, "invalid terminal ready")
	}
	s.phase = clientIdle
	return nil
}

func (s *checkedTerminalStream) validateBeginAck(
	f *tunnel.LifecycleTerminalResponse_BeginAck,
) error {
	v := f.BeginAck
	if s.phase != clientBeginAck || v == nil || v.InvocationId != s.invocation {
		return invalidTerminalResponse()
	}
	if !validSize(v.Rows, v.Columns) {
		return status.Error(codes.InvalidArgument, "invalid terminal size")
	}
	s.phase = clientActive
	return nil
}

func (s *checkedTerminalStream) validateInput(f *tunnel.LifecycleTerminalResponse_Input) error {
	v := f.Input
	if (s.phase != clientActive && s.phase != clientExitAck) || v == nil ||
		v.InvocationId != s.invocation {
		return invalidTerminalResponse()
	}
	if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
		return status.Error(codes.ResourceExhausted, "terminal input frame too large")
	}
	return nil
}

func (s *checkedTerminalStream) validateResize(f *tunnel.LifecycleTerminalResponse_Resize) error {
	v := f.Resize
	if (s.phase != clientActive && s.phase != clientExitAck) || v == nil ||
		v.InvocationId != s.invocation {
		return invalidTerminalResponse()
	}
	if !validSize(v.Rows, v.Columns) {
		return status.Error(codes.InvalidArgument, "invalid terminal size")
	}
	return nil
}

func (s *checkedTerminalStream) validateCancel(f *tunnel.LifecycleTerminalResponse_Cancel) error {
	if s.phase != clientActive || f.Cancel == nil || f.Cancel.InvocationId != s.invocation {
		return invalidTerminalResponse()
	}
	return nil
}

func (s *checkedTerminalStream) validateExitAck(f *tunnel.LifecycleTerminalResponse_ExitAck) error {
	if s.phase != clientExitAck || f.ExitAck == nil || f.ExitAck.InvocationId != s.invocation {
		return invalidTerminalResponse()
	}
	s.phase = clientIdle
	return nil
}

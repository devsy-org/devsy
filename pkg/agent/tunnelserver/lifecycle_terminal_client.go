package tunnelserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type terminalClientPhase uint8

const (
	clientReady terminalClientPhase = iota
	clientIdle
	clientBeginAck
	clientActive
	clientExitAck
	clientClosed
)

type terminalSend struct {
	frame *tunnel.LifecycleTerminalRequest
	done  chan error
}
type terminalClient struct {
	ctx       context.Context
	cancel    context.CancelFunc
	stream    tunnel.Tunnel_LifecycleTerminalClient
	sends     chan terminalSend
	responses chan *tunnel.LifecycleTerminalResponse
	recvDone  chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	active    *terminalAttachment
	closed    bool
	err       error
	term      string
	phase     terminalClientPhase
}
type terminalAttachment struct {
	session    *terminalClient
	id         string
	window     tunnel.TerminalWindow
	input      *terminalInput
	resizes    chan tunnel.TerminalWindow
	canceled   chan struct{}
	cancelOnce sync.Once
	mu         sync.Mutex
	sequence   uint64
	exited     bool
}

func OpenLifecycleTerminal(
	ctx context.Context,
	client tunnel.TunnelClient,
	req provider2.LifecycleTerminalRequest,
) (tunnel.TerminalSession, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := client.LifecycleTerminal(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &terminalClient{
		ctx:       streamCtx,
		cancel:    cancel,
		stream:    stream,
		sends:     make(chan terminalSend),
		responses: make(chan *tunnel.LifecycleTerminalResponse, 1),
		recvDone:  make(chan struct{}),
	}
	s.wg.Add(2)
	go s.sendLoop()
	go s.receiveLoop()
	frame := &tunnel.LifecycleTerminalRequest{
		Frame: &tunnel.LifecycleTerminalRequest_Open{
			Open: &tunnel.LifecycleTerminalOpen{
				ProtocolVersion: req.ProtocolVersion,
				WorkspaceUid:    req.WorkspaceUID,
				Context:         req.Context,
				Nonce:           req.Nonce,
			},
		},
	}
	hctx, stop := context.WithTimeout(ctx, tunnel.LifecycleTerminalHandshakeTimeout)
	defer stop()
	if err = s.send(hctx, frame); err == nil {
		var response *tunnel.LifecycleTerminalResponse
		response, err = s.wait(hctx)
		if err == nil {
			ready := response.GetReady()
			if ready == nil || ready.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion ||
				!ready.Input ||
				!validSize(ready.Rows, ready.Columns) ||
				len(ready.Term) > 256 {
				err = errors.New("invalid lifecycle terminal ready")
			} else {
				s.term = ready.Term
			}
		}
	}
	if err != nil {
		s.shutdown(err)
		return nil, err
	}
	return s, nil
}

func validSize(
	rows, cols uint32,
) bool {
	return rows > 0 && rows <= 65535 && cols > 0 && cols <= 65535
}

func (s *terminalClient) sendLoop() {
	defer s.wg.Done()
	for {
		select {
		case item := <-s.sends:
			err := s.stream.Send(item.frame)
			item.done <- err
			if err != nil {
				s.fail(err)
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *terminalClient) receiveLoop() {
	defer s.wg.Done()
	defer close(s.recvDone)
	for {
		frame, err := s.stream.Recv()
		if err != nil {
			s.fail(err)
			return
		}
		if err = s.handleResponse(frame); err != nil {
			s.fail(err)
			return
		}
	}
}

func (s *terminalClient) handleResponse(frame *tunnel.LifecycleTerminalResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.active
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
		s.term = v.Term
		s.phase = clientIdle
	case *tunnel.LifecycleTerminalResponse_BeginAck:
		v := f.BeginAck
		if s.phase != clientBeginAck || a == nil || v == nil || v.InvocationId != a.id {
			return invalid()
		}
		if !validSize(v.Rows, v.Columns) {
			return status.Error(codes.InvalidArgument, "invalid terminal size")
		}
		s.phase = clientActive
	case *tunnel.LifecycleTerminalResponse_ExitAck:
		v := f.ExitAck
		if s.phase != clientExitAck || a == nil || v == nil || v.InvocationId != a.id {
			return invalid()
		}
		s.phase = clientIdle
	case *tunnel.LifecycleTerminalResponse_Input:
		v := f.Input
		if (s.phase != clientActive && s.phase != clientExitAck) || a == nil || v == nil ||
			v.InvocationId != a.id {
			return invalid()
		}
		if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
			return status.Error(codes.ResourceExhausted, "terminal input frame too large")
		}
		if s.phase == clientExitAck {
			return nil
		}
		return a.input.append(v.Data)
	case *tunnel.LifecycleTerminalResponse_Resize:
		v := f.Resize
		if (s.phase != clientActive && s.phase != clientExitAck) || a == nil || v == nil ||
			v.InvocationId != a.id {
			return invalid()
		}
		if !validSize(v.Rows, v.Columns) {
			return status.Error(codes.InvalidArgument, "invalid terminal size")
		}
		size := tunnel.TerminalWindow{
			Term:    s.term,
			Rows:    uint16(v.Rows),
			Columns: uint16(v.Columns),
		}
		select {
		case a.resizes <- size:
		default:
			select {
			case <-a.resizes:
			default:
			}
			select {
			case a.resizes <- size:
			default:
			}
		}
		return nil
	case *tunnel.LifecycleTerminalResponse_Cancel:
		if s.phase != clientActive || a == nil || f.Cancel == nil || f.Cancel.InvocationId != a.id {
			return invalid()
		}
		return status.Error(codes.Canceled, "lifecycle terminal canceled")
	default:
		return invalid()
	}
	select {
	case s.responses <- frame:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func validTerm(term string) bool {
	if term == "" || len(term) > 128 {
		return false
	}
	for _, c := range term {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' &&
			c != '_' &&
			c != '.' &&
			c != '+' {
			return false
		}
	}
	return true
}

func (s *terminalClient) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	a := s.active
	s.mu.Unlock()
	if a != nil {
		a.input.close(err)
		a.cancelOnce.Do(func() { close(a.canceled) })
	}
	s.cancel()
}

func (s *terminalClient) send(ctx context.Context, frame *tunnel.LifecycleTerminalRequest) error {
	return s.sendItem(ctx, terminalSend{frame: frame, done: make(chan error, 1)})
}

func (s *terminalClient) sendItem(ctx context.Context, item terminalSend) error {
	select {
	case s.sends <- item:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.failure()
	}
	select {
	case err := <-item.done:
		return err
	case <-ctx.Done():
		s.fail(ctx.Err())
		return ctx.Err()
	case <-s.ctx.Done():
		return s.failure()
	}
}

func (s *terminalClient) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	return s.ctx.Err()
}

func (s *terminalClient) wait(ctx context.Context) (*tunnel.LifecycleTerminalResponse, error) {
	select {
	case frame := <-s.responses:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.failure()
	}
}

func (s *terminalClient) shutdown(err error) {
	s.fail(err)
	s.wg.Wait()
}

func (s *terminalClient) Begin(
	ctx context.Context,
	inv tunnel.TerminalInvocation,
) (tunnel.TerminalAttachment, error) {
	if inv.ID == "" || len(inv.ID) > 256 || len(inv.Key) > 256 || inv.Phase != "postStartCommand" ||
		inv.Index < 0 ||
		uint64(inv.Index) > uint64(^uint32(0)) {
		return nil, status.Error(codes.InvalidArgument, "invalid terminal invocation")
	}
	s.mu.Lock()
	if s.closed || s.active != nil || s.err != nil || s.phase != clientIdle {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "terminal session unavailable")
	}
	a := &terminalAttachment{
		session:  s,
		id:       inv.ID,
		input:    newTerminalInput(),
		resizes:  make(chan tunnel.TerminalWindow, 1),
		canceled: make(chan struct{}),
	}
	s.active = a
	s.phase = clientBeginAck
	s.mu.Unlock()
	hctx, cancel := context.WithTimeout(ctx, tunnel.LifecycleTerminalHandshakeTimeout)
	defer cancel()
	err := s.send(
		hctx,
		&tunnel.LifecycleTerminalRequest{
			Frame: &tunnel.LifecycleTerminalRequest_Begin{
				Begin: &tunnel.LifecycleTerminalBegin{
					InvocationId: inv.ID,
					Phase:        inv.Phase,
					Index:        uint32(inv.Index),
					Key:          inv.Key,
				},
			},
		},
	)
	if err == nil {
		var response *tunnel.LifecycleTerminalResponse
		response, err = s.wait(hctx)
		if err == nil {
			ack := response.GetBeginAck()
			if ack == nil || ack.InvocationId != inv.ID || !validSize(ack.Rows, ack.Columns) {
				err = errors.New("invalid terminal begin acknowledgement")
			} else {
				a.window = tunnel.TerminalWindow{
					Term:    s.term,
					Rows:    uint16(ack.Rows),
					Columns: uint16(ack.Columns),
				}
			}
		}
	}
	if err != nil {
		s.shutdown(err)
		return nil, err
	}
	return a, nil
}

func (s *terminalClient) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	active := s.active
	if active == nil {
		s.phase = clientClosed
	}
	s.mu.Unlock()
	if active != nil {
		err := status.Error(codes.FailedPrecondition, "terminal invocation active")
		s.shutdown(err)
		return err
	}
	err := s.send(
		ctx,
		&tunnel.LifecycleTerminalRequest{
			Frame: &tunnel.LifecycleTerminalRequest_Complete{
				Complete: &tunnel.LifecycleTerminalComplete{},
			},
		},
	)
	if err == nil {
		select {
		case <-s.recvDone:
			if !errors.Is(s.failure(), io.EOF) {
				err = s.failure()
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	s.shutdown(io.EOF)
	return err
}
func (a *terminalAttachment) Window() tunnel.TerminalWindow         { return a.window }
func (a *terminalAttachment) Output() io.Writer                     { return a }
func (a *terminalAttachment) Input() io.Reader                      { return a.input }
func (a *terminalAttachment) Resizes() <-chan tunnel.TerminalWindow { return a.resizes }
func (a *terminalAttachment) Canceled() <-chan struct{}             { return a.canceled }
func (a *terminalAttachment) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exited {
		return 0, status.Error(codes.FailedPrecondition, "terminal invocation exited")
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), tunnel.LifecycleTerminalMaxFrame)
		a.sequence++
		err := a.session.send(
			a.session.ctx,
			&tunnel.LifecycleTerminalRequest{
				Frame: &tunnel.LifecycleTerminalRequest_Output{
					Output: &tunnel.LifecycleTerminalOutput{
						InvocationId: a.id,
						Sequence:     a.sequence,
						Data:         append([]byte(nil), p[:n]...),
					},
				},
			},
		)
		if err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (a *terminalAttachment) Exit(ctx context.Context, exit tunnel.TerminalExit) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exited {
		return status.Error(codes.FailedPrecondition, "terminal invocation exited")
	}
	a.exited = true
	a.session.mu.Lock()
	if a.session.phase != clientActive {
		a.session.mu.Unlock()
		return status.Error(codes.FailedPrecondition, "terminal invocation inactive")
	}
	a.session.phase = clientExitAck
	a.session.mu.Unlock()
	a.input.close(io.EOF)
	ectx, cancel := context.WithTimeout(ctx, tunnel.LifecycleTerminalExitAckTimeout)
	defer cancel()
	err := a.session.send(
		ectx,
		&tunnel.LifecycleTerminalRequest{
			Frame: &tunnel.LifecycleTerminalRequest_Exit{
				Exit: &tunnel.LifecycleTerminalExit{
					InvocationId: a.id,
					ExitCode:     int32(exit.ExitCode),
					Signal:       exit.Signal,
					StartError:   exit.StartError,
				},
			},
		},
	)
	if err == nil {
		var frame *tunnel.LifecycleTerminalResponse
		frame, err = a.session.wait(ectx)
		if err == nil && (frame.GetExitAck() == nil || frame.GetExitAck().InvocationId != a.id) {
			err = fmt.Errorf("invalid terminal exit acknowledgement")
		}
	}
	if err != nil {
		a.session.shutdown(err)
		return err
	}
	a.session.mu.Lock()
	a.session.active = nil
	a.session.mu.Unlock()
	return nil
}

type terminalInput struct {
	mu   sync.Mutex
	data []byte
	err  error
	wake chan struct{}
}

func newTerminalInput() *terminalInput { return &terminalInput{wake: make(chan struct{}, 1)} }

func (b *terminalInput) append(p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		if errors.Is(b.err, io.EOF) {
			return nil
		}
		return b.err
	}
	if len(b.data)+len(p) > 256<<10 {
		return status.Error(codes.ResourceExhausted, "lifecycle terminal input buffer full")
	}
	b.data = append(b.data, p...)
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return nil
}

func (b *terminalInput) close(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *terminalInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		b.mu.Lock()
		if len(b.data) > 0 {
			n := copy(p, b.data)
			b.data = b.data[n:]
			b.mu.Unlock()
			return n, nil
		}
		err := b.err
		b.mu.Unlock()
		if err != nil {
			return 0, err
		}
		<-b.wake
	}
}

func (b *terminalInput) Close() error { b.close(io.EOF); return nil }

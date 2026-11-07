package tunnelserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
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
	session      *terminalClient
	id           string
	window       tunnel.TerminalWindow
	input        *terminalInput
	resizes      chan tunnel.TerminalWindow
	canceled     chan struct{}
	cancelOnce   sync.Once
	outputCancel context.CancelFunc
	outputCtx    context.Context
	mu           sync.Mutex
	sequence     uint64
	exited       bool
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
			err = s.acceptReady(response.GetReady())
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

func validTerm(term string) bool {
	if term == "" || len(term) > 128 {
		return false
	}
	for _, c := range term {
		if !validTermCharacter(c) {
			return false
		}
	}
	return true
}

func validTermCharacter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.ContainsRune("-_.+", c)
}

func (s *terminalClient) Begin(
	ctx context.Context,
	inv tunnel.TerminalInvocation,
) (tunnel.TerminalAttachment, error) {
	inv.Key = terminalDisplayKey(inv.Key)
	if !validTerminalInvocation(inv) {
		return nil, status.Error(codes.InvalidArgument, "invalid terminal invocation")
	}
	s.mu.Lock()
	if !s.available() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "terminal session unavailable")
	}
	outputCtx, outputCancel := context.WithCancel(s.ctx)
	a := &terminalAttachment{
		outputCtx: outputCtx, outputCancel: outputCancel,
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
					Index:        terminalIndex(inv.Index),
					Key:          inv.Key,
				},
			},
		},
	)
	if err == nil {
		var response *tunnel.LifecycleTerminalResponse
		response, err = s.wait(hctx)
		if err == nil {
			err = s.acceptBeginAck(a, inv.ID, response.GetBeginAck())
		}
	}
	if err != nil {
		s.shutdown(err)
		return nil, err
	}
	return a, nil
}

func terminalWindow(term string, rows, columns uint32) tunnel.TerminalWindow {
	r, ok := terminalUint16(rows)
	if !ok {
		panic("invalid terminal rows")
	}
	c, ok := terminalUint16(columns)
	if !ok {
		panic("invalid terminal columns")
	}
	return tunnel.TerminalWindow{Term: term, Rows: r, Columns: c}
}

func terminalUint16(v uint32) (uint16, bool) {
	if v > math.MaxUint16 {
		return 0, false
	}
	return uint16(v), true
}

func terminalIndex(v int) uint32 {
	if v < 0 || uint64(v) > math.MaxUint32 {
		panic("invalid terminal index")
	}
	return uint32(v)
}

func terminalExitCode(v int) int32 {
	n := int64(v) % (int64(math.MaxUint32) + 1)
	if n > math.MaxInt32 {
		n -= int64(math.MaxUint32) + 1
	}
	if n < math.MinInt32 {
		n += int64(math.MaxUint32) + 1
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		panic("unreachable exit code")
	}
	return int32(n)
}

func validTerminalInvocation(inv tunnel.TerminalInvocation) bool {
	return inv.ID != "" && len(inv.ID) <= 256 && len(inv.Key) <= 256 &&
		inv.Phase == "postStartCommand" &&
		inv.Index >= 0 &&
		uint64(inv.Index) <= uint64(^uint32(0))
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
	if errors.Is(err, io.EOF) {
		err = nil
	}
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
func (a *terminalAttachment) Input() io.ReadCloser                  { return a.input }
func (a *terminalAttachment) Resizes() <-chan tunnel.TerminalWindow { return a.resizes }
func (a *terminalAttachment) Canceled() <-chan struct{}             { return a.canceled }
func (a *terminalAttachment) Abort()                                { a.outputCancel() }
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
		err := a.session.sendOutput(
			a.outputCtx,
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
	a.Abort()
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
					ExitCode:     terminalExitCode(exit.ExitCode),
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

func queueTerminalResize(resizes chan tunnel.TerminalWindow, size tunnel.TerminalWindow) {
	select {
	case resizes <- size:
	default:
		select {
		case <-resizes:
		default:
		}
		select {
		case resizes <- size:
		default:
		}
	}
}

func (s *terminalClient) acceptReady(ready *tunnel.LifecycleTerminalReady) error {
	if ready == nil || ready.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion ||
		!ready.Input ||
		!validSize(ready.Rows, ready.Columns) ||
		len(ready.Term) > 256 {
		return errors.New("invalid lifecycle terminal ready")
	}
	s.term = ready.Term
	return nil
}

func (s *terminalClient) available() bool {
	return !s.closed && s.active == nil && s.err == nil && s.phase == clientIdle
}

func (s *terminalClient) acceptBeginAck(
	a *terminalAttachment,
	id string,
	ack *tunnel.LifecycleTerminalBeginAck,
) error {
	if ack == nil || ack.InvocationId != id || !validSize(ack.Rows, ack.Columns) {
		return errors.New("invalid terminal begin acknowledgement")
	}
	a.window = terminalWindow(s.term, ack.Rows, ack.Columns)
	return nil
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
	if frame == nil {
		return invalidTerminalResponse()
	}
	enqueue, err := s.dispatchClientResponse(frame)
	if err != nil || !enqueue {
		return err
	}
	select {
	case s.responses <- frame:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *terminalClient) dispatchClientResponse(
	frame *tunnel.LifecycleTerminalResponse,
) (bool, error) {
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalResponse_Ready,
		*tunnel.LifecycleTerminalResponse_BeginAck,
		*tunnel.LifecycleTerminalResponse_ExitAck:
		if err := s.handleControlResponse(frame); err != nil {
			return false, err
		}
	case *tunnel.LifecycleTerminalResponse_Input:
		return false, s.handleInput(f)
	case *tunnel.LifecycleTerminalResponse_Resize:
		return false, s.handleResize(f)
	case *tunnel.LifecycleTerminalResponse_Cancel:
		return false, s.handleCancel(f)
	default:
		return false, invalidTerminalResponse()
	}
	return true, nil
}

func (s *terminalClient) handleControlResponse(frame *tunnel.LifecycleTerminalResponse) error {
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalResponse_Ready:
		return s.handleReady(f)
	case *tunnel.LifecycleTerminalResponse_BeginAck:
		return s.handleBeginAck(f)
	case *tunnel.LifecycleTerminalResponse_ExitAck:
		return s.handleExitAck(f)
	default:
		return invalidTerminalResponse()
	}
}

func (s *terminalClient) handleReady(f *tunnel.LifecycleTerminalResponse_Ready) error {
	v := f.Ready
	if s.phase != clientReady || v == nil {
		return invalidTerminalResponse()
	}
	if v.ProtocolVersion != tunnel.LifecycleTerminalProtocolVersion || !v.Input ||
		!validSize(v.Rows, v.Columns) ||
		!validTerm(v.Term) {
		return status.Error(codes.InvalidArgument, "invalid terminal ready")
	}
	s.term = v.Term
	s.phase = clientIdle
	return nil
}

func (s *terminalClient) handleBeginAck(f *tunnel.LifecycleTerminalResponse_BeginAck) error {
	a := s.active
	v := f.BeginAck
	if s.phase != clientBeginAck || a == nil || v == nil || v.InvocationId != a.id {
		return invalidTerminalResponse()
	}
	if !validSize(v.Rows, v.Columns) {
		return status.Error(codes.InvalidArgument, "invalid terminal size")
	}
	s.phase = clientActive
	return nil
}

func (s *terminalClient) handleExitAck(f *tunnel.LifecycleTerminalResponse_ExitAck) error {
	a := s.active
	v := f.ExitAck
	if s.phase != clientExitAck || a == nil || v == nil || v.InvocationId != a.id {
		return invalidTerminalResponse()
	}
	s.phase = clientIdle
	return nil
}

func (s *terminalClient) handleInput(f *tunnel.LifecycleTerminalResponse_Input) error {
	a := s.active
	v := f.Input
	if (s.phase != clientActive && s.phase != clientExitAck) || a == nil || v == nil ||
		v.InvocationId != a.id {
		return invalidTerminalResponse()
	}
	if len(v.Data) > tunnel.LifecycleTerminalMaxFrame {
		return status.Error(codes.ResourceExhausted, "terminal input frame too large")
	}
	if s.phase == clientExitAck {
		return nil
	}
	return a.input.append(v.Data)
}

func (s *terminalClient) handleResize(f *tunnel.LifecycleTerminalResponse_Resize) error {
	a := s.active
	v := f.Resize
	if (s.phase != clientActive && s.phase != clientExitAck) || a == nil || v == nil ||
		v.InvocationId != a.id {
		return invalidTerminalResponse()
	}
	if !validSize(v.Rows, v.Columns) {
		return status.Error(codes.InvalidArgument, "invalid terminal size")
	}
	size := terminalWindow(s.term, v.Rows, v.Columns)
	queueTerminalResize(a.resizes, size)
	return nil
}

func (s *terminalClient) handleCancel(f *tunnel.LifecycleTerminalResponse_Cancel) error {
	a := s.active
	if s.phase != clientActive || a == nil || f.Cancel == nil || f.Cancel.InvocationId != a.id {
		return invalidTerminalResponse()
	}
	return status.Error(codes.Canceled, "lifecycle terminal canceled")
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
	default:
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

func (s *terminalClient) sendOutput(
	ctx context.Context,
	frame *tunnel.LifecycleTerminalRequest,
) error {
	item := terminalSend{frame: frame, done: make(chan error, 1)}
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
	default:
	}
	select {
	case err := <-item.done:
		return err
	case <-ctx.Done():
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

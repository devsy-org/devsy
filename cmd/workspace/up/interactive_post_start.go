package up

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/terminal"
	"golang.org/x/term"
)

type interactivePostStartOptions struct {
	stdin, stdout  *os.File
	rows, columns  int
	term           string
	status         *interactiveStatusGate
	output         io.Writer
	exitAckTimeout time.Duration
	inputFactory   func(*os.File) (terminal.Input, error)
}

type interactivePostStart struct {
	opts   interactivePostStartOptions
	mu     sync.Mutex
	active bool
	cancel context.CancelFunc
	done   chan struct{}
}

func newInteractivePostStart(opts interactivePostStartOptions) *interactivePostStart {
	if opts.status != nil {
		opts.status.renderMu.Lock()
		opts.status.mu.Lock()
		opts.status.enabled = true
		opts.status.mu.Unlock()
		opts.status.renderMu.Unlock()
	}
	return &interactivePostStart{opts: opts}
}

func (s *interactivePostStart) Close() {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
	if s.opts.status != nil {
		s.opts.status.SetActive(false)
	}
	if s.opts.status != nil && s.opts.status.journal != nil {
		_ = s.opts.status.journal.end(ctx)
	}
}

type terminalReceived struct {
	frame *tunnel.LifecycleTerminalRequest
	err   error
}
type terminalRead struct {
	data []byte
	err  error
}

type interactiveSession struct {
	owner               *interactivePostStart
	ctx                 context.Context
	stream              tunnel.Tunnel_LifecycleTerminalServer
	rows, cols          uint32
	input               terminal.Input
	restore, restoreLog func()
	journalActive       bool
	inputCancel         context.CancelFunc
	done                chan struct{}
	reads               <-chan terminalRead
	invocation          string
	sequence            uint64
	output              chan []byte
	outputDone          chan error
}

func (s *interactivePostStart) Serve(
	parent context.Context,
	stream tunnel.Tunnel_LifecycleTerminalServer,
) error {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return fmt.Errorf("terminal session already active")
	}
	ctx, cancel := context.WithCancel(parent)
	s.active = true
	s.cancel = cancel
	s.done = make(chan struct{})
	doneSession := s.done
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.active = false
		s.cancel = nil
		s.done = nil
		close(doneSession)
		s.mu.Unlock()
	}()
	session := &interactiveSession{owner: s, ctx: ctx, stream: stream}
	return session.serve()
}

func (s *interactivePostStart) size() (uint32, uint32, error) {
	if !validTerminalDimension(s.opts.rows) || !validTerminalDimension(s.opts.columns) ||
		s.opts.stdout == nil {
		return 0, 0, fmt.Errorf("invalid terminal dimensions")
	}
	c, r := terminalSize(s.opts.stdout)
	if s.opts.rows != 0 {
		r = s.opts.rows
	}
	if s.opts.columns != 0 {
		c = s.opts.columns
	}
	return checkedTerminalSize(r, c)
}

func checkedTerminalSize(r, c int) (uint32, uint32, error) {
	if r < 0 || r > 65535 || c < 0 || c > 65535 {
		return 0, 0, fmt.Errorf("invalid terminal dimensions")
	}
	return uint32(r), uint32(c), nil
}

func terminalSize(stdout *os.File) (int, int) {
	c, r, err := term.GetSize(int(stdout.Fd()))
	if err != nil || c < 1 || r < 1 {
		return 80, 24
	}
	return c, r
}

func validTerminalDimension(value int) bool {
	return value >= 0 && value <= 65535
}

func (t *interactiveSession) serve() error {
	if err := t.ready(); err != nil {
		return err
	}
	received := make(chan terminalReceived, 1)
	go t.receive(received)
	resize := ssh.WatchWindowSize(t.ctx)
	defer t.stopOutput()
	defer t.finish()
	for {
		complete, err := t.step(resize, received)
		if err != nil || complete {
			return err
		}
	}
}

func (t *interactiveSession) finish() {
	t.cleanup()
	t.finishJournal()
	if t.owner.opts.status != nil {
		t.owner.opts.status.SetActive(false)
	}
}

func (t *interactiveSession) step(
	resize <-chan os.Signal,
	received <-chan terminalReceived,
) (bool, error) {
	select {
	case <-t.ctx.Done():
		t.sendCancel()
		return true, t.ctx.Err()
	case <-resize:
		return false, t.handleResize()
	case v := <-t.reads:
		return false, t.handleRead(v)
	case v := <-received:
		return t.handleFrame(v)
	}
}

func (t *interactiveSession) ready() error {
	var err error
	t.rows, t.cols, err = t.owner.size()
	if err != nil {
		return err
	}
	name := t.owner.opts.term
	if name == "" {
		name = "xterm-256color"
	}
	if !validInteractiveTerm(name) {
		return fmt.Errorf("invalid TERM")
	}
	return t.stream.Send(
		&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_Ready{
			Ready: &tunnel.LifecycleTerminalReady{
				ProtocolVersion: tunnel.LifecycleTerminalProtocolVersion,
				Term:            name, Rows: t.rows, Columns: t.cols, Input: true,
			},
		}},
	)
}

func (t *interactiveSession) receive(received chan<- terminalReceived) {
	for {
		f, e := t.stream.Recv()
		select {
		case received <- terminalReceived{f, e}:
		case <-t.ctx.Done():
			return
		}
		if e != nil {
			return
		}
	}
}

func (t *interactiveSession) stopOutput() {
	if t.output != nil {
		close(t.output)
		t.output = nil
	}
}

func (t *interactiveSession) cleanup() {
	if t.restore != nil {
		t.restore()
		t.restore = nil
	}
	if t.restoreLog != nil {
		t.restoreLog()
		t.restoreLog = nil
	}
	if t.inputCancel != nil {
		t.inputCancel()
		t.inputCancel = nil
	}
	if t.input != nil {
		t.input.Cancel()
		if t.done != nil {
			<-t.done
		}
		_ = t.input.Close()
		t.input = nil
	}
	t.reads = nil
}

func (t *interactiveSession) finishJournal() {
	if !t.journalActive {
		return
	}
	t.journalActive = false
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = t.owner.opts.status.journal.end(ctx)
}

func (t *interactiveSession) sendCancel() {
	if t.invocation == "" {
		return
	}
	done := make(chan struct{})
	id := t.invocation
	go func() {
		_ = t.stream.Send(
			&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_Cancel{
				Cancel: &tunnel.LifecycleTerminalCancel{InvocationId: id},
			}},
		)
		close(done)
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (t *interactiveSession) handleResize() error {
	if t.invocation == "" {
		return nil
	}
	r, c, err := t.owner.size()
	if err != nil {
		return err
	}
	if r == t.rows && c == t.cols {
		return nil
	}
	t.rows, t.cols = r, c
	return t.stream.Send(
		&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_Resize{
			Resize: &tunnel.LifecycleTerminalResize{
				InvocationId: t.invocation,
				Rows:         r,
				Columns:      c,
			},
		}},
	)
}

func (t *interactiveSession) handleRead(v terminalRead) error {
	if len(v.data) > 0 {
		if err := t.stream.Send(
			&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_Input{
				Input: &tunnel.LifecycleTerminalInput{InvocationId: t.invocation, Data: v.data},
			}},
		); err != nil {
			return err
		}
	}
	if v.err != nil {
		t.sendCancel()
		return v.err
	}
	return nil
}

func (t *interactiveSession) handleFrame(v terminalReceived) (bool, error) {
	if v.err != nil {
		t.sendCancel()
		return false, v.err
	}
	if v.frame == nil {
		return false, io.ErrUnexpectedEOF
	}
	return t.dispatchFrame(v.frame)
}

func (t *interactiveSession) dispatchFrame(frame *tunnel.LifecycleTerminalRequest) (bool, error) {
	switch f := frame.Frame.(type) {
	case *tunnel.LifecycleTerminalRequest_Begin:
		return false, t.begin(f.Begin)
	case *tunnel.LifecycleTerminalRequest_Output:
		return t.writeOutput(f.Output)
	case *tunnel.LifecycleTerminalRequest_Exit:
		return false, t.exit(f.Exit)
	case *tunnel.LifecycleTerminalRequest_Complete:
		return t.complete(f.Complete)
	default:
		return false, fmt.Errorf("unexpected terminal frame")
	}
}

func (t *interactiveSession) complete(frame *tunnel.LifecycleTerminalComplete) (bool, error) {
	if t.invocation != "" || frame == nil {
		return false, fmt.Errorf("invalid complete")
	}
	return true, nil
}

func (t *interactiveSession) begin(begin *tunnel.LifecycleTerminalBegin) error {
	if t.invocation != "" || begin == nil || begin.InvocationId == "" ||
		begin.Phase != "postStartCommand" {
		return fmt.Errorf("invalid begin")
	}
	if err := t.prepareInput(); err != nil {
		return err
	}
	t.invocation = begin.InvocationId
	t.sequence = 0
	t.output = make(chan []byte, 6)
	t.outputDone = make(chan error, 1)
	writer := t.owner.opts.output
	if writer == nil {
		writer = t.owner.opts.stdout
	}
	go terminalOutput(writer, t.output, t.outputDone)
	if err := t.stream.Send(
		&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_BeginAck{
			BeginAck: &tunnel.LifecycleTerminalBeginAck{
				InvocationId: t.invocation,
				Rows:         t.rows,
				Columns:      t.cols,
			},
		}},
	); err != nil {
		return err
	}
	t.startInput()
	return nil
}

func (t *interactiveSession) prepareInput() error {
	var err error
	t.rows, t.cols, err = t.owner.size()
	if err != nil {
		return err
	}
	factory := t.owner.opts.inputFactory
	if factory == nil {
		factory = terminal.NewInput
	}
	t.input, err = factory(t.owner.opts.stdin)
	if err != nil {
		return err
	}
	t.activateStatus()
	if !log.DebugEnabled() {
		t.restoreLog = log.SuppressConsoleOutput()
	}
	t.restore, err = terminal.EnterRaw(t.owner.opts.stdin, t.owner.opts.stdout)
	if err != nil {
		t.cleanup()
		return err
	}
	return nil
}

func (t *interactiveSession) activateStatus() {
	if t.owner.opts.status != nil {
		if t.owner.opts.status.journal != nil {
			t.owner.opts.status.journal.begin()
			t.journalActive = true
		}
		t.owner.opts.status.SetActive(true)
	}
}

func (t *interactiveSession) startInput() {
	readCtx, cancel := terminalInputContext(t.ctx)
	t.inputCancel = cancel
	ch := make(chan terminalRead, 1)
	t.reads = ch
	t.done = make(chan struct{})
	done := t.done
	go func(in terminal.Input) {
		defer close(done)
		for {
			b := make([]byte, tunnel.LifecycleTerminalMaxFrame)
			n, e := in.Read(b)
			select {
			case ch <- terminalRead{b[:n], e}:
			case <-readCtx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}(t.input)
}

func (t *interactiveSession) writeOutput(output *tunnel.LifecycleTerminalOutput) (bool, error) {
	if !t.validOutput(output) {
		return false, fmt.Errorf("invalid output")
	}
	t.sequence++
	data := append([]byte(nil), output.Data...)
	select {
	case t.output <- data:
		return false, nil
	case e := <-t.outputDone:
		return true, e
	case <-t.ctx.Done():
		return true, t.ctx.Err()
	}
}

func (t *interactiveSession) validOutput(output *tunnel.LifecycleTerminalOutput) bool {
	return output != nil && t.invocation != "" && output.InvocationId == t.invocation &&
		len(output.Data) <= tunnel.LifecycleTerminalMaxFrame && output.Sequence == t.sequence+1
}

func (t *interactiveSession) exit(exit *tunnel.LifecycleTerminalExit) error {
	if exit == nil || t.invocation == "" || exit.InvocationId != t.invocation {
		return fmt.Errorf("invalid exit")
	}
	t.stopOutput()
	if err := t.drainOutput(); err != nil {
		return err
	}
	t.cleanup()
	t.finishJournal()
	if t.owner.opts.status != nil {
		t.owner.opts.status.SetActive(false)
	}
	if err := t.stream.Send(
		&tunnel.LifecycleTerminalResponse{Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
			ExitAck: &tunnel.LifecycleTerminalExitAck{InvocationId: t.invocation},
		}},
	); err != nil {
		return err
	}
	t.invocation = ""
	return nil
}

func (t *interactiveSession) drainOutput() error {
	timeout := t.owner.opts.exitAckTimeout
	if timeout <= 0 {
		timeout = tunnel.LifecycleTerminalExitAckTimeout
	}
	timer := time.NewTimer(timeout)
	select {
	case err := <-t.outputDone:
		timer.Stop()
		return err
	case <-timer.C:
		return fmt.Errorf("terminal output drain timed out")
	case <-t.ctx.Done():
		timer.Stop()
		return t.ctx.Err()
	}
}

func terminalInputContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}

func terminalOutput(out io.Writer, chunks <-chan []byte, done chan<- error) {
	for data := range chunks {
		for len(data) > 0 {
			n, err := out.Write(data)
			if err != nil {
				done <- err
				return
			}
			if n == 0 {
				done <- io.ErrShortWrite
				return
			}
			data = data[n:]
		}
	}
	done <- nil
}

func validInteractiveTerm(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !validInteractiveTermChar(c) {
			return false
		}
	}
	return true
}

func validInteractiveTermChar(c rune) bool {
	if asciiRange(c, 'a', 'z') || asciiRange(c, 'A', 'Z') || asciiRange(c, '0', '9') {
		return true
	}
	switch c {
	case '-', '_', '.', '+':
		return true
	default:
		return false
	}
}

func asciiRange(c, first, last rune) bool { return c >= first && c <= last }

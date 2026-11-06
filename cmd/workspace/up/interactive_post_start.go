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
	if s.opts.status != nil && s.opts.status.journal != nil {
		_ = s.opts.status.journal.end(ctx)
	}
}

func (s *interactivePostStart) size() (uint32, uint32, error) {
	if s.opts.rows < 0 || s.opts.rows > 65535 || s.opts.columns < 0 || s.opts.columns > 65535 ||
		s.opts.stdout == nil {
		return 0, 0, fmt.Errorf("invalid terminal dimensions")
	}
	c, r, err := term.GetSize(int(s.opts.stdout.Fd()))
	if err != nil || c < 1 || r < 1 {
		c, r = 80, 24
	}
	if s.opts.rows != 0 {
		r = s.opts.rows
	}
	if s.opts.columns != 0 {
		c = s.opts.columns
	}
	if r > 65535 || c > 65535 {
		return 0, 0, fmt.Errorf("invalid terminal dimensions")
	}
	return uint32(r), uint32(c), nil
}

type terminalReceived struct {
	frame *tunnel.LifecycleTerminalRequest
	err   error
}
type terminalRead struct {
	data []byte
	err  error
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
	rows, cols, err := s.size()
	if err != nil {
		return err
	}
	name := s.opts.term
	if name == "" {
		name = "xterm-256color"
	}
	if !validInteractiveTerm(name) {
		return fmt.Errorf("invalid TERM")
	}
	if err = stream.Send(
		&tunnel.LifecycleTerminalResponse{
			Frame: &tunnel.LifecycleTerminalResponse_Ready{
				Ready: &tunnel.LifecycleTerminalReady{
					ProtocolVersion: tunnel.LifecycleTerminalProtocolVersion,
					Term:            name,
					Rows:            rows,
					Columns:         cols,
					Input:           true,
				},
			},
		},
	); err != nil {
		return err
	}
	received := make(chan terminalReceived, 1)
	go func() {
		for {
			f, e := stream.Recv()
			select {
			case received <- terminalReceived{f, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	resize := ssh.WatchWindowSize(ctx)
	var input terminal.Input
	var restore func()
	var restoreLog func()
	var journalActive bool
	var inputCancel context.CancelFunc
	var done chan struct{}
	var reads <-chan terminalRead
	var invocation string
	var sequence uint64
	var output chan []byte
	var outputDone chan error
	stopOutput := func() {
		if output != nil {
			close(output)
			output = nil
		}
	}
	defer stopOutput()
	cleanup := func() {
		if restore != nil {
			restore()
			restore = nil
		}
		if restoreLog != nil {
			restoreLog()
			restoreLog = nil
		}
		if inputCancel != nil {
			inputCancel()
			inputCancel = nil
		}
		if input != nil {
			input.Cancel()
			if done != nil {
				<-done
			}
			_ = input.Close()
			input = nil
		}
		reads = nil
	}
	finishJournal := func() {
		if !journalActive {
			return
		}
		journalActive = false
		flushCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = s.opts.status.journal.end(flushCtx)
	}
	defer func() {
		cleanup()
		finishJournal()
		if s.opts.status != nil {
			s.opts.status.SetActive(false)
		}
	}()
	sendCancel := func() {
		if invocation == "" {
			return
		}
		done := make(chan struct{})
		id := invocation
		go func() {
			_ = stream.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_Cancel{
						Cancel: &tunnel.LifecycleTerminalCancel{InvocationId: id},
					},
				},
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

	for {
		select {
		case <-ctx.Done():
			sendCancel()
			return ctx.Err()
		case <-resize:
			if invocation == "" {
				continue
			}
			r, c, e := s.size()
			if e != nil {
				return e
			}
			if r == rows && c == cols {
				continue
			}
			rows, cols = r, c
			if e = stream.Send(
				&tunnel.LifecycleTerminalResponse{
					Frame: &tunnel.LifecycleTerminalResponse_Resize{
						Resize: &tunnel.LifecycleTerminalResize{
							InvocationId: invocation,
							Rows:         r,
							Columns:      c,
						},
					},
				},
			); e != nil {
				return e
			}
		case v := <-reads:
			if len(v.data) > 0 {
				if err = stream.Send(
					&tunnel.LifecycleTerminalResponse{
						Frame: &tunnel.LifecycleTerminalResponse_Input{
							Input: &tunnel.LifecycleTerminalInput{
								InvocationId: invocation,
								Data:         v.data,
							},
						},
					},
				); err != nil {
					return err
				}
			}
			if v.err != nil {
				sendCancel()
				return v.err
			}
		case v := <-received:
			if v.err != nil {
				sendCancel()
				return v.err
			}
			if v.frame == nil {
				return io.ErrUnexpectedEOF
			}
			switch f := v.frame.Frame.(type) {
			case *tunnel.LifecycleTerminalRequest_Begin:
				if invocation != "" || f.Begin == nil || f.Begin.InvocationId == "" ||
					f.Begin.Phase != "postStartCommand" {
					return fmt.Errorf("invalid begin")
				}
				rows, cols, err = s.size()
				if err != nil {
					return err
				}
				factory := s.opts.inputFactory
				if factory == nil {
					factory = terminal.NewInput
				}
				input, err = factory(s.opts.stdin)
				if err != nil {
					return err
				}
				if s.opts.status != nil {
					if s.opts.status.journal != nil {
						s.opts.status.journal.begin()
						journalActive = true
					}
					s.opts.status.SetActive(true)
				}
				if !log.DebugEnabled() {
					restoreLog = log.SuppressConsoleOutput()
				}
				restore, err = terminal.EnterRaw(s.opts.stdin, s.opts.stdout)
				if err != nil {
					cleanup()
					return err
				}
				invocation = f.Begin.InvocationId
				sequence = 0
				output = make(chan []byte, 6)
				outputDone = make(chan error, 1)
				writer := s.opts.output
				if writer == nil {
					writer = s.opts.stdout
				}
				go terminalOutput(writer, output, outputDone)
				if err = stream.Send(
					&tunnel.LifecycleTerminalResponse{
						Frame: &tunnel.LifecycleTerminalResponse_BeginAck{
							BeginAck: &tunnel.LifecycleTerminalBeginAck{
								InvocationId: invocation,
								Rows:         rows,
								Columns:      cols,
							},
						},
					},
				); err != nil {
					return err
				}
				var readCtx context.Context
				readCtx, inputCancel = terminalInputContext(ctx)
				ch := make(chan terminalRead, 1)
				reads = ch
				done = make(chan struct{})
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
				}(input)
			case *tunnel.LifecycleTerminalRequest_Output:
				if f.Output == nil || invocation == "" || f.Output.InvocationId != invocation ||
					len(f.Output.Data) > tunnel.LifecycleTerminalMaxFrame ||
					f.Output.Sequence != sequence+1 {
					return fmt.Errorf("invalid output")
				}
				sequence++
				data := append([]byte(nil), f.Output.Data...)
				select {
				case output <- data:
				case e := <-outputDone:
					outputDone = nil
					return e
				case <-ctx.Done():
					return ctx.Err()
				}
			case *tunnel.LifecycleTerminalRequest_Exit:
				if f.Exit == nil || invocation == "" || f.Exit.InvocationId != invocation {
					return fmt.Errorf("invalid exit")
				}
				stopOutput()
				timeout := s.opts.exitAckTimeout
				if timeout <= 0 {
					timeout = tunnel.LifecycleTerminalExitAckTimeout
				}
				timer := time.NewTimer(timeout)
				select {
				case err = <-outputDone:
					timer.Stop()
					if err != nil {
						return err
					}
				case <-timer.C:
					return fmt.Errorf("terminal output drain timed out")
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
				cleanup()
				if err = stream.Send(
					&tunnel.LifecycleTerminalResponse{
						Frame: &tunnel.LifecycleTerminalResponse_ExitAck{
							ExitAck: &tunnel.LifecycleTerminalExitAck{InvocationId: invocation},
						},
					},
				); err != nil {
					return err
				}
				invocation = ""
				finishJournal()
				if s.opts.status != nil {
					s.opts.status.SetActive(false)
				}
			case *tunnel.LifecycleTerminalRequest_Complete:
				if invocation != "" || f.Complete == nil {
					return fmt.Errorf("invalid complete")
				}
				return nil
			default:
				return fmt.Errorf("unexpected terminal frame")
			}
		}
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
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' &&
			c != '_' &&
			c != '.' &&
			c != '+' {
			return false
		}
	}
	return true
}

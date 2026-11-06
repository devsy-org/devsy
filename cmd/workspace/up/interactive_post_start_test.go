package up

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/pty/ptytest"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/terminal"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/suite"
	"golang.org/x/term"
	"google.golang.org/grpc"
)

const (
	testInvocationOne = "one"
	testInvocationTwo = "two"
)

type interactiveStream struct {
	grpc.ServerStream
	ctx        context.Context
	in         chan *tunnel.LifecycleTerminalRequest
	out        chan *tunnel.LifecycleTerminalResponse
	panicBegin bool
}

func (s *interactiveStream) Context() context.Context { return s.ctx }
func (s *interactiveStream) Send(f *tunnel.LifecycleTerminalResponse) error {
	if f.GetBeginAck() != nil && s.panicBegin {
		panic("injected send panic")
	}
	select {
	case s.out <- f:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *interactiveStream) Recv() (*tunnel.LifecycleTerminalRequest, error) {
	select {
	case f, ok := <-s.in:
		if !ok {
			return nil, io.EOF
		}
		return f, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

type InteractivePostStartSuite struct{ suite.Suite }

func TestInteractivePostStartSuite(t *testing.T) { suite.Run(t, new(InteractivePostStartSuite)) }

type interactiveFixture struct {
	device *ptytest.PTY
	file   *os.File
	stream *interactiveStream
	owner  *interactivePostStart
	done   chan error
	cancel context.CancelFunc
}

func beginFrame() *tunnel.LifecycleTerminalRequest {
	return &tunnel.LifecycleTerminalRequest{
		Frame: &tunnel.LifecycleTerminalRequest_Begin{
			Begin: &tunnel.LifecycleTerminalBegin{
				InvocationId: testInvocationOne,
				Phase:        "postStartCommand",
			},
		},
	}
}

func exitFrame() *tunnel.LifecycleTerminalRequest {
	return &tunnel.LifecycleTerminalRequest{
		Frame: &tunnel.LifecycleTerminalRequest_Exit{
			Exit: &tunnel.LifecycleTerminalExit{InvocationId: testInvocationOne, ExitCode: 7},
		},
	}
}

func (s *InteractivePostStartSuite) TestReadyBeforeRawAndCurrentBeginSize() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.Require().NotNil(s.response(f).GetReady())
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
	s.Require().NoError(f.device.Resize(43, 91))
	f.stream.in <- beginFrame()
	ack := s.response(f).GetBeginAck()
	s.Require().NotNil(ack)
	s.Equal(uint32(43), ack.Rows)
	s.Equal(uint32(91), ack.Columns)
}

func (s *InteractivePostStartSuite) TestOutputWithoutNewlineAndInput() {
	f := s.fixture()
	s.begin(f)
	f.stream.in <- &tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Output{
		Output: &tunnel.LifecycleTerminalOutput{InvocationId: testInvocationOne, Sequence: 1, Data: []byte("prompt: ")},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.device.ExpectMatchContext(ctx, "prompt: ")
	f.device.Write('x')
	s.Equal([]byte("x"), s.response(f).GetInput().Data)
}

func (s *InteractivePostStartSuite) TestExitRestoresAndReleasesInput() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	f.stream.in <- exitFrame()
	s.Require().NotNil(s.response(f).GetExitAck())
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
	f.device.WriteLine("later")
	data := make([]byte, 16)
	n, err := f.file.Read(data)
	s.Require().NoError(err)
	s.Equal("later\n", string(data[:n]))
}

func (s *InteractivePostStartSuite) TestInvalidDimensions() {
	owner := newInteractivePostStart(interactivePostStartOptions{stdout: os.Stdout, rows: -1})
	_, _, err := owner.size()
	s.Error(err)
	owner.opts.rows = 0
	owner.opts.columns = 65536
	_, _, err = owner.size()
	s.Error(err)
}

func (s *InteractivePostStartSuite) TestCancelRestores() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	f.owner.Close()
	s.Require().NotNil(s.response(f).GetCancel())
	select {
	case err = <-f.done:
		s.True(errors.Is(err, context.Canceled))
		f.done <- err
	case <-time.After(3 * time.Second):
		s.FailNow("cancel timeout")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
}

type testReporter struct {
	events chan string
	block  <-chan struct{}
}

func (r *testReporter) Report(e status.Event) {
	if r.block != nil {
		<-r.block
	}
	r.events <- e.Step
}

func (s *InteractivePostStartSuite) TestStatusHeldAndFlushedWithoutBlocking() {
	events := make(chan string, 300)
	block := make(chan struct{})
	r := &testReporter{events: events, block: block}
	var out bytes.Buffer
	gate := newInteractiveStatusGate(r, &out, false)
	gate.enabled = true
	gate.SetActive(true)
	for _, v := range []string{"first", "second"} {
		gate.Report(status.Event{Step: v})
	}
	select {
	case <-events:
		s.FailNow("rendered during ownership")
	default:
	}
	gate.Report(status.Event{Step: "third"})
	done := make(chan struct{})
	go func() { gate.SetActive(false); close(done) }()
	select {
	case <-done:
		s.FailNow("drain returned before rendering")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.FailNow("drain timed out")
	}
	s.Equal("first", <-events)
	s.Equal("second", <-events)
	s.Equal("third", <-events)
	gate.Report(status.Event{Step: "ready"})
	s.Equal("ready", <-events)
}

type pausedWriter struct {
	release chan struct{}
	started chan struct{}
}

func (w *pausedWriter) Write(p []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	return len(p), nil
}

func (s *InteractivePostStartSuite) TestPausedOutputLongerThanFiveSeconds() {
	w := &pausedWriter{release: make(chan struct{}), started: make(chan struct{}, 1)}
	f := s.fixture(func(o *interactivePostStartOptions) { o.output = w })
	s.begin(f)
	f.stream.in <- &tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Output{
		Output: &tunnel.LifecycleTerminalOutput{InvocationId: testInvocationOne, Sequence: 1, Data: []byte("prompt")},
	}}
	<-w.started
	f.stream.in <- exitFrame()
	select {
	case <-f.stream.out:
		s.FailNow("early acknowledgement")
	case <-time.After(5100 * time.Millisecond):
	}
	close(w.release)
	s.Require().NotNil(s.response(f).GetExitAck())
}

func (s *InteractivePostStartSuite) TestStreamErrorRestores() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	close(f.stream.in)
	s.Require().NotNil(s.response(f).GetCancel())
	select {
	case err = <-f.done:
		s.Error(err)
		f.done <- err
	case <-time.After(3 * time.Second):
		s.FailNow("stream error timeout")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
}

func (s *InteractivePostStartSuite) TestNoStatusDoesNotSuppressTerminal() {
	f := s.fixture(func(o *interactivePostStartOptions) {
		o.status = newInteractiveStatusGate(status.Nop(), io.Discard, true)
	})
	s.begin(f)
	f.stream.in <- &tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Output{
		Output: &tunnel.LifecycleTerminalOutput{InvocationId: testInvocationOne, Sequence: 1, Data: []byte("visible")},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.device.ExpectMatchContext(ctx, "visible")
}

type eofInput struct{}

func (eofInput) Read([]byte) (int, error) { return 0, io.EOF }
func (eofInput) Close() error             { return nil }
func (eofInput) Cancel() bool             { return true }
func (s *InteractivePostStartSuite) TestLocalEOFSendsCancel() {
	f := s.fixture(func(o *interactivePostStartOptions) {
		o.inputFactory = func(*os.File) (terminal.Input, error) { return eofInput{}, nil }
	})
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	s.Require().NotNil(s.response(f).GetCancel())
	select {
	case err = <-f.done:
		s.ErrorIs(err, io.EOF)
		f.done <- err
	case <-time.After(time.Second):
		s.FailNow("EOF timeout")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
}

func (s *InteractivePostStartSuite) TestOutputTimeoutRestores() {
	w := &pausedWriter{release: make(chan struct{}), started: make(chan struct{}, 1)}
	f := s.fixture(
		func(o *interactivePostStartOptions) { o.output = w; o.exitAckTimeout = 50 * time.Millisecond },
	)
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	f.stream.in <- &tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Output{
		Output: &tunnel.LifecycleTerminalOutput{InvocationId: testInvocationOne, Sequence: 1, Data: []byte("x")},
	}}
	<-w.started
	f.stream.in <- exitFrame()
	select {
	case err = <-f.done:
		s.ErrorContains(err, "timed out")
		f.done <- err
	case <-time.After(time.Second):
		s.FailNow("timeout not enforced")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
	close(w.release)
}

func (s *InteractivePostStartSuite) TestJournalContinuesWhilePresenterHeld() {
	events := make(chan string, 4)
	j := &testReporter{events: events}
	held := make(chan string, 4)
	gate := newInteractiveStatusGate(&testReporter{events: held}, io.Discard, false)
	reporter := &journalStatusReporter{presenter: gate, journal: j, gate: gate}
	reporter.begin()
	gate.enabled = true
	gate.SetActive(true)
	reporter.Report(status.Event{Step: testInvocationOne})
	reporter.Report(status.Event{Step: testInvocationTwo})
	s.Equal(testInvocationOne, <-events)
	s.Equal(testInvocationTwo, <-events)
	select {
	case <-held:
		s.FailNow("rendered early")
	default:
	}
	gate.SetActive(false)
	s.Equal(testInvocationOne, <-held)
	s.Equal(testInvocationTwo, <-held)
}

func (s *InteractivePostStartSuite) TestPanicRestores() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.Require().NotNil(s.response(f).GetReady())
	f.stream.panicBegin = true
	f.stream.in <- beginFrame()
	select {
	case err = <-f.done:
		s.ErrorContains(err, "panic")
		f.done <- err
	case <-time.After(time.Second):
		s.FailNow("panic timeout")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
}

func (s *InteractivePostStartSuite) TestInvalidOutputRestores() {
	f := s.fixture()
	before, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.begin(f)
	f.stream.in <- &tunnel.LifecycleTerminalRequest{Frame: &tunnel.LifecycleTerminalRequest_Output{
		Output: &tunnel.LifecycleTerminalOutput{InvocationId: testInvocationOne, Sequence: 2, Data: []byte("bad")},
	}}
	select {
	case err = <-f.done:
		s.ErrorContains(err, "invalid output")
		f.done <- err
	case <-time.After(time.Second):
		s.FailNow("error timeout")
	}
	after, err := term.GetState(int(f.file.Fd()))
	s.Require().NoError(err)
	s.True(reflect.DeepEqual(before, after))
}

type eventWriter struct{ lines chan string }

func (w eventWriter) Write(p []byte) (int, error) { w.lines <- string(p); return len(p), nil }
func (s *InteractivePostStartSuite) TestStatusOverflowReportsOmissions() {
	events := make(chan string, 256)
	lines := make(chan string, 1)
	gate := newInteractiveStatusGate(
		&testReporter{events: events},
		eventWriter{lines: lines},
		false,
	)
	gate.enabled = true
	gate.SetActive(true)
	for i := range 300 {
		gate.Report(status.Event{Step: fmt.Sprint(i)})
	}
	gate.SetActive(false)
	for i := range 256 {
		select {
		case event := <-events:
			s.Equal(fmt.Sprint(i), event)
		case <-time.After(time.Second):
			s.FailNow("event timeout")
		}
	}
	select {
	case line := <-lines:
		s.Equal("44 progress updates omitted\n", line)
	case <-time.After(time.Second):
		s.FailNow("omission timeout")
	}
}

func (s *InteractivePostStartSuite) TestBlockedJournalDoesNotBlockHandlers() {
	events := make(chan string, 300)
	block := make(chan struct{})
	gate := newInteractiveStatusGate(status.Nop(), io.Discard, true)
	gate.enabled = true
	gate.SetActive(true)
	reporter := &journalStatusReporter{
		presenter: gate,
		journal:   &testReporter{events: events, block: block},
		gate:      gate,
	}
	reporter.begin()
	done := make(chan struct{})
	go func() {
		for i := range 300 {
			reporter.Report(status.Event{Step: fmt.Sprint(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		s.FailNow("status handler blocked")
	}
	close(block)
	for i := range 300 {
		select {
		case event := <-events:
			s.Equal(fmt.Sprint(i), event)
		case <-time.After(time.Second):
			s.FailNow("journal timeout")
		}
	}
	gate.SetActive(false)
}

func (s *InteractivePostStartSuite) TestStatusFlushesBeforeExitAck() {
	events := make(chan string, 2)
	block := make(chan struct{})
	gate := newInteractiveStatusGate(&testReporter{events: events, block: block}, io.Discard, false)
	f := s.fixture(func(o *interactivePostStartOptions) { o.status = gate })
	s.begin(f)
	gate.Report(status.Event{Step: testInvocationOne})
	gate.Report(status.Event{Step: testInvocationTwo})
	select {
	case <-events:
		s.FailNow("rendered while terminal owned")
	default:
	}
	f.stream.in <- exitFrame()
	select {
	case <-f.stream.out:
		close(block)
		s.FailNow("acknowledgement before status flush")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	s.Require().NotNil(s.response(f).GetExitAck())
	s.Equal(testInvocationOne, <-events)
	s.Equal(testInvocationTwo, <-events)
}

func (s *InteractivePostStartSuite) TestNoninteractiveJournalIsSynchronousAndOrdered() {
	events := make(chan string, 2)
	block := make(chan struct{})
	reporter := &journalStatusReporter{
		presenter: status.Nop(),
		journal:   &testReporter{events: events, block: block},
	}
	finished := make(chan struct{})
	go func() { reporter.Report(status.Event{Step: "first"}); close(finished) }()
	select {
	case <-finished:
		s.FailNow("journal returned before append")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	select {
	case <-finished:
	case <-time.After(time.Second):
		s.FailNow("journal did not finish")
	}
	reporter.Report(status.Event{Step: "second"})
	s.Equal("first", <-events)
	s.Equal("second", <-events)
}

func (s *InteractivePostStartSuite) TestCloseWaitsForJournalDrain() {
	events := make(chan string, 2)
	block := make(chan struct{})
	gate := newInteractiveStatusGate(status.Nop(), io.Discard, true)
	reporter := &journalStatusReporter{
		presenter: gate,
		journal:   &testReporter{events: events, block: block},
		gate:      gate,
	}
	gate.journal = reporter
	f := s.fixture(func(o *interactivePostStartOptions) { o.status = gate })
	s.begin(f)
	reporter.Report(status.Event{Step: testInvocationOne})
	reporter.Report(status.Event{Step: testInvocationTwo})
	closed := make(chan struct{})
	go func() { f.owner.Close(); close(closed) }()
	select {
	case <-closed:
		s.FailNow("close returned before drain")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	select {
	case <-closed:
	case <-time.After(time.Second):
		s.FailNow("close did not drain")
	}
	s.Equal(testInvocationOne, <-events)
	s.Equal(testInvocationTwo, <-events)
}

func (s *InteractivePostStartSuite) TestSequentialBeginAfterJournalDrain() {
	events := make(chan string, 1)
	block := make(chan struct{})
	gate := newInteractiveStatusGate(status.Nop(), io.Discard, true)
	reporter := &journalStatusReporter{
		presenter: gate,
		journal:   &testReporter{events: events, block: block},
		gate:      gate,
	}
	gate.journal = reporter
	f := s.fixture(func(o *interactivePostStartOptions) { o.status = gate })
	s.begin(f)
	reporter.Report(status.Event{Step: testInvocationOne})
	f.stream.in <- exitFrame()
	select {
	case <-f.stream.out:
		close(block)
		s.FailNow("acknowledgement before journal drain")
	case <-time.After(2 * time.Second):
	}
	close(block)
	s.Require().NotNil(s.response(f).GetExitAck())
	s.Equal(testInvocationOne, <-events)
	next := beginFrame()
	next.GetBegin().InvocationId = testInvocationTwo
	started := time.Now()
	f.stream.in <- next
	select {
	case response := <-f.stream.out:
		s.Require().NotNil(response.GetBeginAck())
		s.Equal(testInvocationTwo, response.GetBeginAck().InvocationId)
		s.Less(time.Since(started), 5*time.Second)
	case <-time.After(5 * time.Second):
		s.FailNow("second begin acknowledgement timed out")
	}
}

func (s *InteractivePostStartSuite) TestJournalDrainTimeout() {
	events := make(chan string, 1)
	block := make(chan struct{})
	reporter := &journalStatusReporter{
		presenter: status.Nop(),
		journal:   &testReporter{events: events, block: block},
	}
	reporter.begin()
	reporter.Report(status.Event{Step: testInvocationOne})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.ErrorIs(reporter.end(ctx), context.DeadlineExceeded)
	close(block)
	s.Equal(testInvocationOne, <-events)
}

func (s *InteractivePostStartSuite) TestDefaultLoggerSilentDuringOwnership() {
	old := os.Stderr
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	os.Stderr = w
	log.Init(log.Config{})
	defer func() { os.Stderr = old; log.Init(log.Config{}); _ = r.Close(); _ = w.Close() }()
	f := s.fixture()
	s.begin(f)
	log.Error("must be hidden during ownership")
	f.stream.in <- exitFrame()
	s.Require().NotNil(s.response(f).GetExitAck())
	log.Error("visible after ownership")
	s.Require().NoError(w.Close())
	data, err := io.ReadAll(r)
	s.Require().NoError(err)
	s.NotContains(string(data), "must be hidden during ownership")
	s.Contains(string(data), "visible after ownership")
}

func (s *InteractivePostStartSuite) TestUnsupportedRoutesBeforePreparation() {
	for _, tc := range []struct {
		name     string
		client   client2.BaseWorkspaceClient
		platform bool
	}{
		{name: "platform", platform: true},
		{name: "proxy", client: interactiveProxyClient{}},
		{name: "daemon", client: interactiveDaemonClient{}},
	} {
		s.Run(tc.name, func() {
			calls := 0
			cmd := &UpCmd{
				GlobalFlags:          &flags.GlobalFlags{},
				InteractivePostStart: true,
				SecretsFile:          "/missing/secrets",
				resolveWorkspace: func(
					context.Context, *config.Config, workspace.ResolveParams,
				) (client2.BaseWorkspaceClient, error) {
					calls++
					return tc.client, nil
				},
			}
			cmd.Platform.Enabled = tc.platform
			_, err := cmd.prepareClient(context.Background(), testConfig(), nil)
			s.Require().ErrorContains(err, "unsupported")
			if tc.platform {
				s.Zero(calls)
			} else {
				s.Equal(1, calls)
			}
		})
	}
}

func (s *InteractivePostStartSuite) TestCloseDrainsStatus() {
	events := make(chan string, 2)
	block := make(chan struct{})
	gate := newInteractiveStatusGate(&testReporter{events: events, block: block}, io.Discard, false)
	owner := newInteractivePostStart(interactivePostStartOptions{status: gate})
	gate.SetActive(true)
	gate.Report(status.Event{Step: "held"})
	done := make(chan struct{})
	go func() { owner.Close(); close(done) }()
	select {
	case <-done:
		s.FailNow("close returned before drain")
	case <-time.After(50 * time.Millisecond):
	}
	close(block)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.FailNow("close timed out")
	}
	s.Equal("held", <-events)
	gate.Report(status.Event{Step: "ready"})
	s.Equal("ready", <-events)
}

func (s *InteractivePostStartSuite) fixture(
	opts ...func(*interactivePostStartOptions),
) *interactiveFixture {
	device := ptytest.New(s.T())
	file, err := os.OpenFile(device.Name(), os.O_RDWR, 0)
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &interactiveStream{
		ctx: ctx,
		in:  make(chan *tunnel.LifecycleTerminalRequest, 16),
		out: make(chan *tunnel.LifecycleTerminalResponse, 16),
	}
	options := interactivePostStartOptions{stdin: file, stdout: file}
	for _, apply := range opts {
		apply(&options)
	}
	f := &interactiveFixture{
		device: device,
		file:   file,
		stream: stream,
		owner:  newInteractivePostStart(options),
		done:   make(chan error, 1),
		cancel: cancel,
	}
	go func() {
		defer func() {
			if p := recover(); p != nil {
				f.done <- fmt.Errorf("panic: %v", p)
			}
		}()
		f.done <- f.owner.Serve(ctx, stream)
	}()
	s.T().Cleanup(func() {
		cancel()
		select {
		case <-f.done:
		case <-time.After(3 * time.Second):
			s.T().Error("owner did not stop")
		}
		_ = file.Close()
	})
	return f
}

func (s *InteractivePostStartSuite) response(
	f *interactiveFixture,
) *tunnel.LifecycleTerminalResponse {
	s.T().Helper()
	select {
	case v := <-f.stream.out:
		return v
	case <-time.After(3 * time.Second):
		s.FailNow("response timeout")
		return nil
	}
}

func (s *InteractivePostStartSuite) begin(f *interactiveFixture) {
	s.Require().NotNil(s.response(f).GetReady())
	f.stream.in <- beginFrame()
	s.Require().NotNil(s.response(f).GetBeginAck())
}

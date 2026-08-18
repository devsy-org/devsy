package inject

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util/iojoin"
)

const (
	statusDone           = "done"
	defaultInjectTimeout = 20 * time.Second
)

//go:embed inject.sh
var Script string

type ExecFunc func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error

type LocalFile func(arm bool) (io.ReadCloser, error)

type injectResult struct {
	wasExecuted bool
	err         error
}

type InjectOptions struct {
	Exec         ExecFunc
	LocalFile    LocalFile
	ScriptParams *Params
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Timeout      time.Duration
}

// Inject runs the inject.sh handshake and bridges the caller's stdio to the exec's stdio.
// It returns true if the handshake completed and the exec was started, false if the handshake
// failed and the exec was not started.
func Inject(ctx context.Context, opts InjectOptions) (bool, error) {
	if opts.Exec == nil {
		return false, fmt.Errorf("inject: Exec is required")
	}
	if opts.ScriptParams == nil {
		return false, fmt.Errorf("inject: ScriptParams is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultInjectTimeout
	}
	start := time.Now()
	log.Debugf("start inject")
	defer func() { log.Debugf("complete elapsed=%s", time.Since(start)) }()

	logPreferredAgentDownloadURL(opts.ScriptParams)
	scriptRawCode, err := GenerateScript(Script, opts.ScriptParams)
	if err != nil {
		return true, err
	}

	log.Debug("execute inject script")
	defer log.Debug("done injecting")
	sess, err := newSession(opts, scriptRawCode, start)
	if err != nil {
		return true, err
	}
	defer sess.Close()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()

	return sess.run(execCtx, opts.Timeout)
}

type pipeEnds struct {
	r *os.File
	w *os.File
}

func (p pipeEnds) Close() {
	_ = p.r.Close()
	_ = p.w.Close()
}

type session struct {
	opts          InjectOptions
	script        string
	delayedStderr *delayedWriter
	start         time.Time
	stdin         pipeEnds
	stdout        pipeEnds
}

func newSession(opts InjectOptions, script string, start time.Time) (*session, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdin := pipeEnds{r: stdinR, w: stdinW}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdin.Close() // don't leak the first pair on partial open
		return nil, err
	}
	stdout := pipeEnds{r: stdoutR, w: stdoutW}

	return &session{
		opts:          opts,
		script:        script,
		delayedStderr: newDelayedWriter(opts.Stderr),
		start:         start,
		stdin:         stdin,
		stdout:        stdout,
	}, nil
}

func (s *session) Close() {
	s.stdin.Close()
	s.stdout.Close()
}

// run executes the inject.sh handshake and bridges the caller's stdio to the exec's stdio.
func (s *session) run(ctx context.Context, handshakeTimeout time.Duration) (bool, error) {
	execErrChan := make(chan error, 1)
	go s.runExec(ctx, execErrChan)

	injectChan := make(chan injectResult, 1)
	go func() {
		defer log.Debug("done inject")
		wasExecuted, err := s.handshakeAndBridge(ctx, handshakeTimeout)
		injectChan <- injectResult{
			wasExecuted: wasExecuted,
			err:         command.WrapCommandError(s.delayedStderr.Buffer(), err),
		}
	}()

	result, execErr := resolveRunResult(execErrChan, injectChan)
	log.Debugf("payload delivered elapsed=%s", time.Since(s.start))

	if result.err != nil {
		return result.wasExecuted, result.err
	}
	if execErr != nil && result.wasExecuted {
		return result.wasExecuted, execErr
	}
	return result.wasExecuted, nil
}

// resolveRunResult waits for execErrChan and/or injectChan and decides which
// to trust. execErrChan's send in runExec always happens-before s.stdout.w's
// deferred Close(), which is what unblocks handshakeAndBridge's pipeStreams
// and lets injectChan fire -- so whenever injectChan's wasExecuted is true,
// execErrChan is already populated by the time injectChan is read here.
// select doesn't prefer whichever channel became ready earlier once both are
// ready, so without the inner non-blocking receive a real exec error could
// be silently dropped in favor of a false "succeeded" result.
func resolveRunResult(
	execErrChan <-chan error,
	injectChan <-chan injectResult,
) (injectResult, error) {
	var result injectResult
	var execErr error
	select {
	case execErr = <-execErrChan:
		result = <-injectChan
	case result = <-injectChan:
		select {
		case execErr = <-execErrChan:
		default:
		}
	}
	return result, execErr
}

func (s *session) runExec(ctx context.Context, execErrChan chan<- error) {
	defer func() { _ = s.stdout.w.Close() }()
	defer log.Debug("done exec")

	err := s.opts.Exec(ctx, s.script, s.stdin.r, s.stdout.w, s.delayedStderr)
	if err != nil && !errors.Is(err, context.Canceled) &&
		!strings.Contains(err.Error(), "signal: ") {
		execErrChan <- command.WrapCommandError(s.delayedStderr.Buffer(), err)
	} else {
		execErrChan <- nil
	}
}

// handshakeAndBridge performs the inject.sh handshake and then bridges the
// caller's stdio to the exec's stdio.
func (s *session) handshakeAndBridge(
	ctx context.Context,
	handshakeTimeout time.Duration,
) (bool, error) {
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	injectStart := time.Now()

	// inject.sh announces readiness with "ping".
	line, err := readLine(handshakeCtx, s.stdout.r)
	if err != nil {
		return false, fmt.Errorf("read ping: %w", err)
	}
	if strings.TrimSpace(line) != "ping" {
		return false, fmt.Errorf("unexpected start line %q", line)
	}
	if _, err := s.stdin.w.Write([]byte("pong\n")); err != nil {
		return false, fmt.Errorf("write pong: %w", err)
	}
	log.Debugf("handshake complete elapsed=%s", time.Since(injectStart))

	line, err = readLine(handshakeCtx, s.stdout.r)
	if err != nil {
		return false, fmt.Errorf("read inject signal: %w", err)
	}
	log.Debugf("received line after pong: line=%s", line)

	signal := strings.TrimSpace(line)
	if isBinaryRequest(signal) {
		log.Debug("inject binary")
		if err := s.sendBinary(handshakeCtx, signal); err != nil {
			return false, err
		}
	} else if signal != statusDone {
		return false, fmt.Errorf("unexpected message during inject %q", signal)
	}

	return pipeStreams(streamPipeConfig{
		stdin:         s.stdin.w,
		hostStdin:     s.opts.Stdin,
		hostStdout:    s.opts.Stdout,
		stdout:        s.stdout.r,
		delayedStderr: s.delayedStderr,
	})
}

func logPreferredAgentDownloadURL(params *Params) {
	if !params.PreferAgentDownload {
		return
	}
	url := ""
	if params.DownloadURLs != nil {
		url = params.DownloadURLs.Base
	}
	log.Debugf("prefer downloading agent from URL: url=%s", url)
}

func isBinaryRequest(signal string) bool {
	return strings.HasPrefix(signal, "ARM-")
}

func binaryReaderFor(localFile LocalFile, signal string) (io.ReadCloser, error) {
	isArm := strings.TrimPrefix(signal, "ARM-") == config.BoolTrue
	return localFile(isArm)
}

// sizer is implemented by *os.File; to detect when the binary reader is a file
// to avoid reading the whole file into memory to get its size.
type sizer interface {
	Stat() (os.FileInfo, error)
}

// sendBinary transfers the agent binary to inject.sh using length-prefixed
// framing.
func (s *session) sendBinary(ctx context.Context, signal string) error {
	r, err := binaryReaderFor(s.opts.LocalFile, signal)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer func() { _ = r.Close() }()

	size, ok := binarySize(r)
	if !ok {
		buf, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read binary: %w", err)
		}
		if err := writeFramed(s.stdin.w, int64(len(buf)), bytes.NewReader(buf)); err != nil {
			return err
		}
		return awaitDone(ctx, s.stdout.r)
	}

	if err := writeFramed(s.stdin.w, size, r); err != nil {
		return err
	}
	return awaitDone(ctx, s.stdout.r)
}

// binarySize returns the size of the binary if r is a *os.File, otherwise false.
func binarySize(r io.Reader) (int64, bool) {
	f, ok := r.(sizer)
	if !ok {
		return 0, false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	return info.Size(), true
}

// writeFramed writes a length-prefixed payload to stdin.
func writeFramed(stdin io.Writer, size int64, payload io.Reader) error {
	if _, err := fmt.Fprintf(stdin, "%d\n", size); err != nil {
		return fmt.Errorf("write binary size: %w", err)
	}
	if _, err := io.Copy(stdin, payload); err != nil {
		return fmt.Errorf("write binary payload: %w", err)
	}
	return nil
}

func awaitDone(ctx context.Context, stdout io.ReadCloser) error {
	line, err := readLine(ctx, stdout)
	if err != nil {
		return fmt.Errorf("read binary done: %w", err)
	}
	if strings.TrimSpace(line) != statusDone {
		return fmt.Errorf("unexpected line during inject %q", line)
	}
	return nil
}

type streamPipeConfig struct {
	stdin         io.WriteCloser
	hostStdin     io.Reader
	hostStdout    io.Writer
	stdout        io.ReadCloser
	delayedStderr *delayedWriter
}

func pipeStreams(cfg streamPipeConfig) (bool, error) {
	hostStdout := cfg.hostStdout
	hostStdin := cfg.hostStdin
	if hostStdout == nil {
		hostStdout = io.Discard
	}
	if hostStdin == nil {
		hostStdin = bytes.NewReader(nil)
	}

	cfg.delayedStderr.Start()
	return true, pipe(
		cfg.stdin, hostStdin,
		hostStdout, cfg.stdout,
	)
}

// readLine reads a single line from r, returning the line without the trailing
// newline. It is bound to ctx so a hung inject.sh aborts instead of hanging the
// caller.
func readLine(ctx context.Context, r io.ReadCloser) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		one := make([]byte, 1)
		for {
			n, err := r.Read(one)
			if n > 0 {
				if one[0] == '\n' {
					ch <- result{b.String(), nil}
					return
				}
				b.WriteByte(one[0])
			}
			if err != nil {
				ch <- result{b.String(), err}
				return
			}
		}
	}()

	select {
	case res := <-ch:
		return res.line, res.err
	case <-ctx.Done():
		_ = r.Close()        // Abort: close r so the read goroutine unblocks and exits.
		go func() { <-ch }() // nolint:govet // drain goroutine on r's terminal error
		return "", ctx.Err()
	}
}

const pipeSecondDirTimeout = 5 * time.Second

// pipe runs the two directions of a duplex bridge concurrently, returning once
// both have completed or the slower side is abandoned after timeout.
func pipe(
	toStdin io.WriteCloser, fromStdin io.Reader,
	toStdout io.Writer, fromStdout io.ReadCloser,
) error {
	stdoutSide := func() error {
		_, err := io.Copy(toStdout, fromStdout)
		return err
	}
	stdinSide := func() error {
		_, err := io.Copy(toStdin, fromStdin)
		return err
	}

	stdinErr, stdoutErr := iojoin.Join(stdinSide, stdoutSide, pipeSecondDirTimeout, nil)

	_ = toStdin.Close()
	_ = fromStdout.Close()

	if stdinErr != nil {
		return stdinErr
	}
	return stdoutErr
}

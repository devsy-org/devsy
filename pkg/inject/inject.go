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
)

// Deprecated: Script embeds inject.sh which is deprecated. Platform-native AgentDelivery
// implementations (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
//
//go:embed inject.sh
var Script string

// Deprecated: ExecFunc is part of the legacy shell injection path. Platform-native AgentDelivery
// implementations (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
type ExecFunc func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error

// Deprecated: LocalFile is part of the legacy shell injection path. Platform-native AgentDelivery
// implementations (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
type LocalFile func(arm bool) (io.ReadCloser, error)

type injectResult struct {
	wasExecuted bool
	err         error
}

// Deprecated: InjectOptions is part of the legacy shell injection path. Platform-native AgentDelivery
// implementations (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
type InjectOptions struct {
	Ctx          context.Context
	Exec         ExecFunc
	LocalFile    LocalFile
	ScriptParams *Params
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Timeout      time.Duration
}

// Deprecated: Inject is part of the legacy shell injection path. Platform-native AgentDelivery
// implementations (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
func Inject(opts InjectOptions) (bool, error) {
	if err := validateInjectOptions(opts); err != nil {
		return false, err
	}

	start := time.Now()
	log.Infof("injection: start")
	defer func() { log.Infof("injection: complete elapsed=%s", time.Since(start)) }()

	logPreferAgentDownload(opts.ScriptParams)

	scriptRawCode, err := GenerateScript(Script, opts.ScriptParams)
	if err != nil {
		return true, err
	}

	log.Debug("execute inject script")
	defer log.Debug("done injecting")

	// start script
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return true, err
	}
	defer func() { _ = stdinWriter.Close() }()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return true, err
	}

	// delayed stderr
	delayedStderr := newDelayedWriter(opts.Stderr)

	// check if context is done
	select {
	case <-opts.Ctx.Done():
		return true, context.Canceled
	default:
	}

	// create cancel context
	cancelCtx, cancel := context.WithCancel(opts.Ctx)
	defer cancel()

	// start execution of inject.sh
	execErrChan := make(chan error, 1)
	go runInjectScript(cancelCtx, runInjectScriptParams{
		opts:          opts,
		script:        scriptRawCode,
		stdin:         stdinReader,
		stdout:        stdoutWriter,
		delayedStderr: delayedStderr,
		execErrChan:   execErrChan,
	})

	// inject file
	injectChan := make(chan injectResult, 1)
	go func() {
		defer func() { _ = stdinWriter.Close() }()
		defer log.Debug("done inject")

		wasExecuted, err := inject(
			opts.LocalFile,
			stdinWriter,
			opts.Stdin,
			stdoutReader,
			opts.Stdout,
			delayedStderr,
			opts.Timeout,
		)
		injectChan <- injectResult{
			wasExecuted: wasExecuted,
			err:         command.WrapCommandError(delayedStderr.Buffer(), err),
		}
	}()

	return awaitInjectResult(awaitInjectResultParams{
		opts:          opts,
		start:         start,
		execErrChan:   execErrChan,
		injectChan:    injectChan,
		delayedStderr: delayedStderr,
	})
}

func validateInjectOptions(opts InjectOptions) error {
	if opts.Ctx == nil {
		return fmt.Errorf("context is required")
	}
	if opts.Exec == nil {
		return fmt.Errorf("exec function is required")
	}
	if opts.ScriptParams == nil {
		return fmt.Errorf("script params is required")
	}
	return nil
}

func logPreferAgentDownload(params *Params) {
	if !params.PreferAgentDownload {
		return
	}
	url := ""
	if params.DownloadURLs != nil {
		url = params.DownloadURLs.Base
	}
	log.Debugf("prefer downloading agent from URL: url=%s", url)
}

type runInjectScriptParams struct {
	opts          InjectOptions
	script        string
	stdin         io.Reader
	stdout        io.WriteCloser
	delayedStderr *delayedWriter
	execErrChan   chan<- error
}

func runInjectScript(ctx context.Context, p runInjectScriptParams) {
	defer func() { _ = p.stdout.Close() }()
	defer log.Debug("done exec")

	err := p.opts.Exec(ctx, p.script, p.stdin, p.stdout, p.delayedStderr)
	if err != nil && !errors.Is(err, context.Canceled) &&
		!strings.Contains(err.Error(), "signal: ") {
		p.execErrChan <- command.WrapCommandError(p.delayedStderr.Buffer(), err)
	} else {
		p.execErrChan <- nil
	}
}

type awaitInjectResultParams struct {
	opts          InjectOptions
	start         time.Time
	execErrChan   chan error
	injectChan    chan injectResult
	delayedStderr *delayedWriter
}

func awaitInjectResult(p awaitInjectResultParams) (bool, error) {
	// wait here
	var result injectResult
	var err error
	select {
	case err = <-p.execErrChan:
		result = <-p.injectChan
	case result = <-p.injectChan:
		// we don't wait for the command termination here and will just retry on error
	}
	log.Debugf("injection: payload delivered elapsed=%s", time.Since(p.start))

	if result.err != nil {
		return result.wasExecuted, result.err
	}

	// Exec EOF during binary injection is expected: the container entrypoint
	// may exec the daemon (killing the docker exec process) before inject.sh
	// prints its final response. Only surface exec errors when a command was
	// actually executed through the script.
	if err != nil && result.wasExecuted {
		return result.wasExecuted, err
	}

	if result.wasExecuted || p.opts.ScriptParams.Command == "" {
		return result.wasExecuted, nil
	}

	log.Debugf("Rerun command as binary was injected")
	p.delayedStderr.Start()
	return true, p.opts.Exec(
		p.opts.Ctx,
		p.opts.ScriptParams.Command,
		p.opts.Stdin,
		p.opts.Stdout,
		p.delayedStderr,
	)
}

func inject(
	localFile LocalFile,
	stdin io.WriteCloser,
	stdinOut io.Reader,
	stdout io.ReadCloser,
	stdoutOut io.Writer,
	delayedStderr *delayedWriter,
	timeout time.Duration,
) (bool, error) {
	injectStart := time.Now()

	// wait until we read start
	var line string
	errChan := make(chan error)
	go func() {
		var err error
		line, err = readLine(stdout)
		errChan <- err
	}()

	// wait for line to be read
	err := waitForMessage(errChan, timeout)
	if err != nil {
		return false, err
	}

	err = performMutualHandshake(line, stdin)
	if err != nil {
		return false, err
	}
	log.Debugf("injection: handshake complete elapsed=%s", time.Since(injectStart))

	// wait until we read something
	line, err = readLine(stdout)
	if err != nil {
		return false, err
	}
	log.Debugf("received line after pong: line=%s", line)

	lineStr := strings.TrimSpace(line)
	if isInjectingOfBinaryNeeded(lineStr) {
		log.Debugf("inject binary")
		defer log.Debugf("done injecting binary")

		if err := injectBinaryPayload(localFile, lineStr, stdin, stdout); err != nil {
			return false, err
		}
		// start exec with command
		return false, nil
	}
	if lineStr != "done" {
		return false, fmt.Errorf("unexpected message during inject %s", lineStr)
	}

	return pipeStreams(pipeStreamsParams{
		stdin:         stdin,
		stdinOut:      stdinOut,
		stdoutOut:     stdoutOut,
		stdout:        stdout,
		delayedStderr: delayedStderr,
	})
}

func injectBinaryPayload(
	localFile LocalFile,
	lineStr string,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
) error {
	fileReader, err := getFileReader(localFile, lineStr)
	if err != nil {
		return err
	}
	defer func() { _ = fileReader.Close() }()

	if err := injectBinary(fileReader, stdin, stdout); err != nil {
		return err
	}
	_ = stdout.Close()
	return nil
}

type pipeStreamsParams struct {
	stdin         io.WriteCloser
	stdinOut      io.Reader
	stdoutOut     io.Writer
	stdout        io.ReadCloser
	delayedStderr *delayedWriter
}

func pipeStreams(p pipeStreamsParams) (bool, error) {
	stdoutOut := p.stdoutOut
	stdinOut := p.stdinOut
	if stdoutOut == nil {
		stdoutOut = io.Discard
	}
	if stdinOut == nil {
		stdinOut = bytes.NewReader(nil)
	}

	// now pipe reader into stdout
	p.delayedStderr.Start()
	return true, pipe(
		p.stdin, stdinOut,
		stdoutOut, p.stdout,
	)
}

func isInjectingOfBinaryNeeded(lineStr string) bool {
	return strings.HasPrefix(lineStr, "ARM-")
}

func getFileReader(localFile LocalFile, lineStr string) (io.ReadCloser, error) {
	isArm := strings.TrimPrefix(lineStr, "ARM-") == config.BoolTrue
	return localFile(isArm)
}

func performMutualHandshake(line string, stdin io.WriteCloser) error {
	// check for string
	if strings.TrimSpace(line) != "ping" {
		return fmt.Errorf("unexpected start line %v", line)
	}

	// send our response
	_, err := stdin.Write([]byte("pong\n"))
	if err != nil {
		return fmt.Errorf("write to stdin: %w", err)
	}

	// successful handshake
	return nil
}

func injectBinary(
	fileReader io.ReadCloser,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
) error {
	// copy into writer
	_, err := io.Copy(stdin, fileReader)
	if err != nil {
		return err
	}

	// close stdin
	_ = stdin.Close()

	// wait for done
	line, err := readLine(stdout)
	if err != nil {
		return err
	} else if strings.TrimSpace(line) != "done" {
		return fmt.Errorf("unexpected line during inject %s", line)
	}
	return nil
}

func waitForMessage(errChannel chan error, timeout time.Duration) error {
	select {
	case err := <-errChannel:
		return err
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

func readLine(reader io.Reader) (string, error) {
	// we always only read a single byte
	buf := make([]byte, 1)
	str := ""
	for {
		n, err := reader.Read(buf)
		switch {
		case err != nil:
			return "", err
		case n == 0:
			continue
		case buf[0] == '\n':
			return str, nil
		}

		str += string(buf)
	}
}

const pipeSecondDirTimeout = 5 * time.Second

func pipe(
	toStdin io.WriteCloser, fromStdin io.Reader,
	toStdout io.Writer, fromStdout io.ReadCloser,
) error {
	stdinErr := make(chan error, 1)
	stdoutErr := make(chan error, 1)

	go func() {
		_, err := io.Copy(toStdout, fromStdout)
		stdoutErr <- err
	}()
	go func() {
		_, err := io.Copy(toStdin, fromStdin)
		stdinErr <- err
	}()

	// Wait for whichever direction completes first.
	var firstErr error
	var otherCh <-chan error
	select {
	case firstErr = <-stdinErr:
		otherCh = stdoutErr
	case firstErr = <-stdoutErr:
		otherCh = stdinErr
	}

	// Give the other direction time to finish naturally so we can
	// capture any real error and avoid interrupting data in flight.
	// If it doesn't finish in time, close pipes to force completion.
	var secondErr error
	timer := time.NewTimer(pipeSecondDirTimeout)
	defer timer.Stop()
	select {
	case secondErr = <-otherCh:
	case <-timer.C:
	}

	_ = toStdin.Close()
	_ = fromStdout.Close()

	if firstErr != nil {
		return firstErr
	}
	return secondErr
}

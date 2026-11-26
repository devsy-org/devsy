//go:build !windows

package server

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/ssh"
	"github.com/stretchr/testify/require"
)

const execTestShell = "/bin/sh"

const execTestTerminal = "xterm"

type commandLogSession struct {
	ssh.Session
	output bytes.Buffer
	stderr bytes.Buffer
	input  io.Reader
}

func (s *commandLogSession) Read(p []byte) (int, error) {
	if s.input != nil {
		return s.input.Read(p)
	}
	return 0, io.EOF
}

func (s *commandLogSession) Write(p []byte) (int, error) { return s.output.Write(p) }

func (s *commandLogSession) Stderr() io.ReadWriter { return &s.stderr }

func (*commandLogSession) Signals(chan<- ssh.Signal) {}

func (*commandLogSession) DisablePTYEmulation() {}

type synchronizedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestCommandExecutionDoesNotLogShellArguments(t *testing.T) {
	log.Init(log.Config{Verbosity: 2})
	t.Cleanup(func() { log.Init(log.Config{}) })

	debugLog := &synchronizedLogBuffer{}
	removeSink := log.AddSink(debugLog)
	t.Cleanup(removeSink)

	const canary = "DEVSY_REMOTE_COMMAND_SECRET_CANARY"
	command := "TOKEN=" + canary + "; export TOKEN; printf '%s\\n' \"$TOKEN\""
	for _, testCase := range []struct {
		name string
		pty  bool
	}{
		{name: "without PTY"},
		{name: "with PTY", pty: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &commandLogSession{}
			if testCase.pty {
				input, writer := io.Pipe()
				session.input = input
				t.Cleanup(func() { _ = writer.Close(); _ = input.Close() })
			}
			cmd := exec.Command(execTestShell, "-c", command)
			var err error
			if testCase.pty {
				err = execPTY(ptyExecParams{
					sess: session,
					ptyReq: ssh.Pty{
						Term:   execTestTerminal,
						Window: ssh.Window{Height: 24, Width: 80},
					},
					cmd: cmd,
				})
			} else {
				err = execNonPTY(session, cmd)
			}

			require.NoError(t, err)
			require.Contains(t, session.output.String(), canary)
			require.NotContains(t, debugLog.String(), canary)
			require.Contains(t, debugLog.String(), "execute SSH server")
			require.False(t, strings.Contains(debugLog.String(), command))
		})
	}
}

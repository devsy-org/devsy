package workspace

import (
	"context"
	"io"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type execPTYTerminal struct {
	width, height int
	fd            int
	restore       func()
}

func requestExecPTY(
	ctx context.Context,
	session *ssh.Session,
	stdin io.Reader,
) (func(), error) {
	terminal, err := prepareExecPTYTerminal(stdin)
	if err != nil {
		return nil, err
	}
	if err := session.RequestPty(
		"xterm-256color",
		terminal.height,
		terminal.width,
		ssh.TerminalModes{},
	); err != nil {
		terminal.restore()
		return nil, err
	}
	if terminal.fd < 0 {
		return terminal.restore, nil
	}

	watchCtx, cancelWatch := context.WithCancel(ctx)
	windowChanges, stopWindowWatcher := newExecWindowSizeWatcher(watchCtx)
	stopResizeForwarder := forwardExecWindowChanges(
		watchCtx,
		windowChanges,
		func() (int, int, error) { return term.GetSize(terminal.fd) },
		session.WindowChange,
	)
	var cleanupOnce sync.Once
	return func() {
		cleanupOnce.Do(func() {
			cancelWatch()
			stopResizeForwarder()
			stopWindowWatcher()
			terminal.restore()
		})
	}, nil
}

func prepareExecPTYTerminal(stdin io.Reader) (execPTYTerminal, error) {
	terminal := execPTYTerminal{
		width:   80,
		height:  24,
		fd:      -1,
		restore: func() {},
	}
	input, ok := stdin.(*os.File)
	if !ok {
		return terminal, nil
	}
	fd := int(input.Fd()) // #nosec G115 -- fd comes from an open file.
	if !term.IsTerminal(fd) {
		return terminal, nil
	}
	terminal.fd = fd
	if width, height, err := term.GetSize(fd); err == nil {
		terminal.width, terminal.height = width, height
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return execPTYTerminal{}, err
	}
	terminal.restore = func() { _ = term.Restore(fd, state) }
	return terminal, nil
}

func forwardExecWindowChanges(
	parent context.Context,
	windowChanges <-chan os.Signal,
	getSize func() (width, height int, err error),
	windowChange func(height, width int) error,
) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-windowChanges:
				if !ok {
					return
				}
				if ctx.Err() != nil {
					return
				}
				width, height, err := getSize()
				if err == nil {
					_ = windowChange(height, width)
				}
			}
		}
	}()

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(cancel)
		<-done
	}
}

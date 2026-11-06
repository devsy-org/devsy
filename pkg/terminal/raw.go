package terminal

import (
	"io"
	"os"
	"slices"
	"sync"

	"github.com/devsy-org/devsy/pkg/pty"
)

var restores = struct {
	sync.Mutex
	items []func()
}{}

type Input interface {
	io.Reader
	Cancel() bool
	Close() error
}

func EnterRaw(stdin, stdout *os.File) (func(), error) {
	if stdin == nil || stdout == nil {
		return nil, ErrNotAttached
	}
	inState, err := pty.MakeInputRaw(stdin.Fd())
	if err != nil {
		return nil, err
	}
	outState, err := pty.MakeOutputRaw(stdout.Fd())
	if err != nil {
		_ = pty.RestoreTerminal(stdin.Fd(), inState)
		return nil, err
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			_ = pty.RestoreTerminal(stdout.Fd(), outState)
			_ = pty.RestoreTerminal(stdin.Fd(), inState)
		})
	}
	restores.Lock()
	restores.items = append(restores.items, restore)
	restores.Unlock()
	return restore, nil
}

func RestoreAll() {
	restores.Lock()
	items := restores.items
	restores.items = nil
	restores.Unlock()
	for _, item := range slices.Backward(items) {
		item()
	}
}

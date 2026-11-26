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
	items []*restoreEntry
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
	restore := registerRestore(func() {
		_ = pty.RestoreTerminal(stdout.Fd(), outState)
		_ = pty.RestoreTerminal(stdin.Fd(), inState)
	})
	return restore, nil
}

type restoreEntry struct {
	once sync.Once
	fn   func()
}

func registerRestore(fn func()) func() {
	entry := &restoreEntry{fn: fn}
	restore := func() {
		entry.once.Do(func() {
			restores.Lock()
			for i, item := range restores.items {
				if item == entry {
					restores.items = slices.Delete(restores.items, i, i+1)
					break
				}
			}
			restores.Unlock()
			entry.fn()
		})
	}
	restores.Lock()
	restores.items = append(restores.items, entry)
	restores.Unlock()
	return restore
}

func RestoreAll() {
	restores.Lock()
	items := restores.items
	restores.items = nil
	restores.Unlock()
	for _, item := range slices.Backward(items) {
		item.once.Do(item.fn)
	}
}

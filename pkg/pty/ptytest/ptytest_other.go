//go:build !windows

package ptytest

import "github.com/devsy-org/devsy/pkg/pty"

func newTestPTY(opts ...pty.Option) (pty.PTY, error) {
	return pty.New(opts...)
}

//go:build !windows

package ghcredentials

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/pkg/log"
)

func runGH(binary string, args, env []string, owner *os.File) int {
	//nolint:gosec // The user chose this executable and these unchanged arguments.
	cmd := exec.Command(binary, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	if owner != nil {
		cmd.ExtraFiles = []*os.File{owner}
	}
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		log.Errorw("gh relay: executable unavailable", "error_type", errorKind(err))
		return 126
	}
	done := make(chan struct{})
	defer close(done)
	go forwardSignals(cmd.Process, signals, done)
	if err := cmd.Wait(); err != nil {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return cmd.ProcessState.ExitCode()
	}
	return 0
}

func forwardSignals(process *os.Process, signals <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case sig := <-signals:
			_ = process.Signal(sig)
		case <-done:
			return
		}
	}
}

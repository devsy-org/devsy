//go:build !windows

package shell

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func PrepareProviderShellForeground(parentPID int) (cleanup func(), err error) {
	if parentPID <= 0 || os.Getppid() != parentPID {
		return nil, fmt.Errorf("provider shell parent mismatch")
	}
	signals := providerTerminalSignals()
	ch := make(chan os.Signal, 3)
	cleanup = func() { signal.Stop(ch) }
	if len(signals) > 0 {
		signal.Notify(ch, signals...)
	}
	group, err := syscall.Getpgid(parentPID)
	if err == nil {
		err = syscall.Setpgid(0, group)
	}
	if err == nil && os.Getppid() != parentPID {
		err = fmt.Errorf("provider shell parent mismatch")
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

func providerTerminalSignals() []os.Signal {
	var signals []os.Signal
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			signals = append(signals, sig)
		}
	}
	return signals
}

func newProviderShellParentMonitor(parentPID int) (alive func() bool, cleanup func(), err error) {
	if parentPID <= 0 || os.Getppid() != parentPID {
		return nil, nil, fmt.Errorf("provider shell parent mismatch")
	}
	return func() bool { return os.Getppid() == parentPID }, func() {}, nil
}

//go:build linux || darwin || unix

package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

const launchLeaseEnv = "DEVSY_INTERNAL_LAUNCH_LEASE_FD"

// LaunchLease survives launcher death until the worker exits, including the
// interval before its process reference has been published.
type LaunchLease struct{ file *os.File }

func NewLaunchLease(path string) (*LaunchLease, error) {
	f, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR,
		0o600,
	) // #nosec G304 -- validated task-store lease path
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &LaunchLease{file: f}, nil
}

func (l *LaunchLease) Close() error { return l.file.Close() }

func (l *LaunchLease) attach(cmd *exec.Cmd) {
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, l.file)
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, launchLeaseEnv+"="+strconv.Itoa(fd))
}

// RetainLaunchLease keeps the inherited claim until process exit, without
// passing it on to commands the worker executes.
func RetainLaunchLease() error {
	value := os.Getenv(launchLeaseEnv)
	if value == "" {
		return errors.New("detached worker launch lease is missing")
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return errors.New("invalid detached worker launch lease")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	unix.CloseOnExec(fd)
	return os.Unsetenv(launchLeaseEnv)
}

// CleanupUnpublishedLaunch cannot signal an unidentified process. An available
// lifetime lease proves neither the launcher nor its bootstrap child remains.
func CleanupUnpublishedLaunch(path, _ string, _ bool) error {
	lease, err := NewLaunchLease(path)
	if err != nil {
		return fmt.Errorf("detached launch still owns its lifetime lease: %w", err)
	}
	return lease.Close()
}

func startOwnedBackground(
	cmd *exec.Cmd,
	_ string,
	_ bool,
	prepared func(string) error,
) (func(), error) {
	prepareBackgroundTree(cmd)
	if prepared != nil {
		if err := prepared(""); err != nil {
			return nil, err
		}
	}
	return func() {}, cmd.Start()
}

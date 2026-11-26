package ghcredentials

import (
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/gofrs/flock"
)

type ownerLock struct {
	file *os.File
	lock *flock.Flock
}

func tryOwner(path string) (*ownerLock, bool, error) {
	l := flock.New(path)
	ok, err := l.TryLock()
	if err != nil || !ok {
		_ = l.Close()
		return nil, ok, err
	}
	return &ownerLock{lock: l}, true, nil
}

func (l *ownerLock) Close() error { return l.lock.Close() }

func runGH(binary string, args, env []string, _ *os.File) int {
	//nolint:gosec // The user chose this executable and these unchanged arguments.
	cmd := exec.Command(binary, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode()
		}
		log.Errorw("gh relay: executable unavailable", "error_type", errorKind(err))
		return 126
	}
	return 0
}

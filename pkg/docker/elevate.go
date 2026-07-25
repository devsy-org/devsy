package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
)

// elevationAuthTimeout is generous enough for interactive credential entry,
// unlike the short per-command probe timeouts.
const elevationAuthTimeout = 2 * time.Minute

// Supported privilege-elevation helpers.
const (
	elevationPkexec = "pkexec"
	elevationSudo   = "sudo"
	elevationDoas   = "doas"
)

// Elevator runs docker commands through a privilege-elevation helper (pkexec,
// sudo, doas). It authenticates once, up front, so an operation's many commands
// share a single prompt via the warmed OS credential cache.
type Elevator struct {
	prefix []string // elevation command and leading args; always non-empty

	once sync.Once
	err  error
}

// ElevatorFromName maps a helper name to an Elevator; "" and "none" return
// (nil, nil), unknown names error.
func ElevatorFromName(name string) (*Elevator, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none":
		return nil, nil
	case elevationPkexec:
		return &Elevator{prefix: []string{elevationPkexec}}, nil
	case elevationSudo:
		return &Elevator{prefix: []string{elevationSudo}}, nil
	case elevationDoas:
		return &Elevator{prefix: []string{elevationDoas}}, nil
	default:
		return nil, fmt.Errorf(
			"unknown privilege elevation %q (want pkexec, sudo, doas, or none)",
			name,
		)
	}
}

// wrap builds the elevated invocation of dockerCommand. env (KEY=VAL entries,
// e.g. DOCKER_HOST) is forwarded through env(1) because sudo/pkexec/doas reset
// the child environment and would otherwise drop provider configuration.
func (e *Elevator) wrap(dockerCommand string, env, args []string) (string, []string) {
	full := make([]string, 0, len(e.prefix)+len(env)+len(args)+1)
	full = append(full, e.prefix[1:]...)
	if len(env) > 0 {
		full = append(full, "env")
		full = append(full, env...)
	}
	full = append(full, dockerCommand)
	full = append(full, args...)
	return e.prefix[0], full
}

// ensureAuthenticated warms the credential cache once. It elevates the
// client-only "--version" (no daemon needed) on its own timeout, so a short
// caller deadline cannot kill the prompt.
func (e *Elevator) ensureAuthenticated(dockerCommand string, env []string) error {
	e.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), elevationAuthTimeout)
		defer cancel()

		name, args := e.wrap(dockerCommand, env, []string{"--version"})
		//nolint:gosec // command and args come from trusted provider config
		cmd := exec.CommandContext(ctx, name, args...)
		if env != nil {
			cmd.Env = append(os.Environ(), env...)
		}
		cmd.Stdin = os.Stdin // attach terminal for the credential prompt
		cmd.Stdout = io.Discard
		cmd.Stderr = os.Stderr

		log.Debugf("authenticating privilege elevation via %s", e.prefix[0])
		if err := cmd.Run(); err != nil {
			e.err = fmt.Errorf("privilege elevation via %s failed: %w", e.prefix[0], err)
		}
	})
	return e.err
}

// EnsureElevated authenticates the configured elevator once; concurrent callers
// block on the single prompt. No-op and safe when no elevator is set.
func (r *DockerHelper) EnsureElevated() error {
	if r.Elevator == nil {
		return nil
	}
	return r.Elevator.ensureAuthenticated(r.DockerCommand, r.Environment)
}

package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

func ValidateInteractivePostStart(hooks []types.LifecycleHook) error {
	for i, hook := range hooks {
		count := 0
		for _, command := range hook {
			if len(command) > 0 {
				count++
			}
		}
		if count > 1 {
			return fmt.Errorf(
				"%s merged index %d has multiple nonempty named commands",
				PhasePostStart,
				i,
			)
		}
	}
	return nil
}

func runInteractiveHook(p hookRunParams) error {
	if p.content != "" {
		skipped, err := markerExists(p.name, p.content)
		if err != nil {
			p.recordAll(workspacelogs.InvocationFailed, reasonMarkerCheckFailed)
			return err
		}
		if skipped {
			p.recordSkipped(reasonAlreadyAttempted)
			return nil
		}
	}
	launches, err := prepareInteractiveHooks(p)
	if err != nil {
		return err
	}
	if p.content != "" {
		if err := writeMarker(p.name, p.content); err != nil {
			p.recordAll(workspacelogs.InvocationFailed, reasonMarkerCheckFailed)
			return err
		}
	}
	return executeInteractiveHooks(p, launches)
}

func executeInteractive(p hookRunParams, command hookCommand, launch interactiveLaunch) error {
	inv := p.invocation(p.ctx, command.key)
	attachment, err := p.terminal.Begin(
		p.ctx,
		tunnel.TerminalInvocation{
			ID:    workspacelogs.NewID(),
			Phase: string(PhasePostStart),
			Index: p.index,
			Key:   command.key,
		},
	)
	if err != nil {
		inv.record(workspacelogs.InvocationFailed, reasonStartFailed)
		return fmt.Errorf("%s: terminal begin: %w", p.name, err)
	}
	started := time.Now()
	inv.record(workspacelogs.InvocationRunning, "")
	result, runErr := runInteractiveProcess(p.ctx, p, launch, attachment)
	result.Duration = time.Since(started)
	finishCtx := p.ctx
	if errors.Is(runErr, context.Canceled) && finishCtx.Err() == nil {
		var cancelFinish context.CancelFunc
		finishCtx, cancelFinish = context.WithCancel(finishCtx)
		cancelFinish()
	}
	inv.finish(finishCtx, result, runErr)
	exit := tunnel.TerminalExit{ExitCode: result.ExitCode, Signal: result.Signal}
	if result.ExitCode < 0 && result.Signal == "" && runErr != nil {
		exit.StartError = "failed to start terminal command"
	}
	exitCtx, cancel := context.WithTimeout(
		context.Background(),
		tunnel.LifecycleTerminalExitAckTimeout,
	)
	logTerminalExit(attachment.Exit(exitCtx, exit))
	cancel()
	if runErr == nil {
		return nil
	}
	return interactiveError(p, command, result, runErr)
}

func logTerminalExit(err error) {
	if err != nil {
		log.Warnf("lifecycle terminal exit: %v", err)
	}
}

func prepareInteractiveHooks(p hookRunParams) (map[int]interactiveLaunch, error) {
	launches := make(map[int]interactiveLaunch)
	for i, hook := range p.commands {
		for _, command := range hook {
			if len(command) == 0 {
				continue
			}
			launch, err := prepareInteractiveLaunch(p.ctx, command, p.env.remoteUser)
			if err != nil {
				p.index = i
				p.recordAll(workspacelogs.InvocationFailed, reasonStartFailed)
				return nil, fmt.Errorf("%s: %w", p.name, err)
			}
			launches[i] = launch
		}
	}
	return launches, nil
}

func interactiveError(
	p hookRunParams,
	command hookCommand,
	result subprocess.Result,
	runErr error,
) error {
	display := strings.Join(command.c, " ")
	if p.redactor != nil {
		display = p.redactor.Redact(display)
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return fmt.Errorf("%s: command %q canceled", p.name, display)
	}
	if result.Signal != "" {
		return fmt.Errorf("%s: command %q failed: signal %s", p.name, display, result.Signal)
	}
	if result.ExitCode >= 0 {
		return fmt.Errorf("%s: command %q failed: exit code %d", p.name, display, result.ExitCode)
	}
	return fmt.Errorf("%s: command %q failed to start", p.name, display)
}

func executeInteractiveHooks(p hookRunParams, launches map[int]interactiveLaunch) error {
	for i, hook := range p.commands {
		p.index = i
		for key, command := range hook {
			if len(command) == 0 {
				p.invocation(p.ctx, key).record(workspacelogs.InvocationSkipped, reasonNoCommand)
				continue
			}
			if err := executeInteractive(
				p,
				hookCommand{key: key, c: command},
				launches[i],
			); err != nil {
				p.recordFrom(i+1, workspacelogs.InvocationSkipped, reasonNotReached)
				return err
			}
		}
	}
	return nil
}

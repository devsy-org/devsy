package clientimplementation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

const providerCancellationGrace = 10 * time.Second

func runProviderWithCancellation(parent context.Context, opts RunCommandOptions) error {
	return runProviderWithCancellationUsingPublisher(parent, opts, publishProviderCancellation)
}

func runProviderWithCancellationUsingPublisher(
	parent context.Context,
	opts RunCommandOptions,
	publish func(string) error,
) (retErr error) {
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	if len(opts.Command) == 0 {
		return nil
	}

	dir, err := createProviderInvocationDir()
	if err != nil {
		return fmt.Errorf("create provider cancellation directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			retErr = errors.Join(
				retErr,
				fmt.Errorf("remove provider cancellation directory: %w", sanitizeMarkerError(err)),
			)
		}
	}()

	marker := filepath.Join(dir, "cancel")
	opts.Environ = providerCancellationEnvironment(opts.Environ, marker)
	execution, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancel(context.Canceled)
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	completed := make(chan error, 1)
	go func() {
		completed <- runCommandExecution(execution, opts)
	}()
	return waitForProviderCancellation(
		parent,
		marker,
		publish,
		providerExecution{cancel: cancel, completed: completed},
	)
}

func providerCancellationEnvironment(original []string, marker string) []string {
	if original == nil {
		original = os.Environ()
	}
	environ := make([]string, 0, len(original)+1)
	for _, kv := range original {
		key, _, ok := strings.Cut(kv, "=")
		if ok && providerReservedKey(key) {
			continue
		}
		environ = append(environ, kv)
	}
	return append(environ, config.EnvProviderCancelFile+"="+marker)
}

type providerExecution struct {
	cancel    context.CancelCauseFunc
	completed <-chan error
}

func waitForProviderCancellation(
	parent context.Context,
	marker string,
	publish func(string) error,
	process providerExecution,
) error {
	select {
	case result := <-process.completed:
		if parent.Err() != nil {
			return context.Cause(parent)
		}
		return result
	case <-parent.Done():
	}

	cause := context.Cause(parent)
	if err := publish(marker); err != nil {
		process.cancel(cause)
		<-process.completed
		return errors.Join(
			cause,
			fmt.Errorf("publish provider cancellation marker: %w", sanitizeMarkerError(err)),
		)
	}
	return waitForProviderGrace(cause, process)
}

func waitForProviderGrace(cause error, process providerExecution) error {
	timer := time.NewTimer(providerCancellationGrace)
	defer timer.Stop()
	select {
	case <-process.completed:
		return cause
	case <-timer.C:
		process.cancel(cause)
		<-process.completed
		return cause
	}
}

func providerReservedKey(key string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(key, config.EnvProviderCancelFile)
	}
	return key == config.EnvProviderCancelFile
}

func publishProviderCancellation(marker string) (retErr error) {
	root, err := os.OpenRoot(filepath.Dir(marker))
	if err != nil {
		return fmt.Errorf("cannot open marker directory: %w", sanitizeMarkerError(err))
	}
	defer func() {
		if err := root.Close(); err != nil {
			retErr = errors.Join(retErr, sanitizeMarkerError(err))
		}
	}()
	f, err := root.OpenFile("cancel", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return sanitizeMarkerError(f.Close())
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("cannot create marker: %w", sanitizeMarkerError(err))
	}
	info, statErr := root.Lstat("cancel")
	if statErr != nil {
		return fmt.Errorf("cannot inspect existing marker: %w", sanitizeMarkerError(statErr))
	}
	if !info.Mode().IsRegular() {
		return errors.New("existing marker is not a regular file")
	}
	return nil
}

func sanitizeMarkerError(err error) error {
	if err == nil {
		return nil
	}
	if pathErr, ok := errors.AsType[*os.PathError](err); ok {
		return pathErr.Err
	}
	return errors.New("marker filesystem operation failed")
}

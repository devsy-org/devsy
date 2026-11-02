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
) error {
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
	defer os.RemoveAll(dir)

	marker := filepath.Join(dir, "cancel")
	environ := make([]string, 0, len(opts.Environ)+1)
	for _, kv := range opts.Environ {
		key, _, ok := strings.Cut(kv, "=")
		if ok && providerReservedKey(key) {
			continue
		}
		environ = append(environ, kv)
	}
	opts.Environ = append(environ, config.EnvProviderCancelFile+"="+marker)

	execution, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancel(context.Canceled)
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	completed := make(chan error, 1)
	go func() {
		completed <- runCommandExecution(execution, opts)
	}()

	select {
	case result := <-completed:
		if parent.Err() != nil {
			return context.Cause(parent)
		}
		return result
	case <-parent.Done():
	}

	cause := context.Cause(parent)
	if err := publish(marker); err != nil {
		cancel(cause)
		<-completed
		return errors.Join(
			cause,
			fmt.Errorf("publish provider cancellation marker: %w", sanitizeMarkerError(err)),
		)
	}

	timer := time.NewTimer(providerCancellationGrace)
	defer timer.Stop()
	select {
	case <-completed:
		return cause
	case <-timer.C:
		cancel(cause)
		<-completed
		return cause
	}
}

func providerReservedKey(key string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(key, config.EnvProviderCancelFile)
	}
	return key == config.EnvProviderCancelFile
}

func publishProviderCancellation(marker string) error {
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return sanitizeMarkerError(f.Close())
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("cannot create marker: %w", sanitizeMarkerError(err))
	}
	info, statErr := os.Lstat(marker)
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
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return errors.New("marker filesystem operation failed")
}

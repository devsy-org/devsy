package clientimplementation

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

const (
	providerCancellationGrace = 10 * time.Second
	providerForceAckWait      = 5 * time.Second
)

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
	if len(opts.Command) == 1 {
		opts.providerShell, err = prepareProviderShell(dir, opts.Command[0])
		if err != nil {
			return err
		}
	}
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
		providerExecution{cancel: cancel, completed: completed, shell: opts.providerShell},
	)
}

func prepareProviderShell(dir, script string) (*providerShellInvocation, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("generate provider shell acknowledgement token: %w", err)
	}
	files := &providerShellInvocation{
		script:   filepath.Join(dir, "script"),
		force:    filepath.Join(dir, "force"),
		ack:      filepath.Join(dir, "ack"),
		ackToken: hex.EncodeToString(token[:]),
	}
	if err := writeProviderScript(dir, script); err != nil {
		return nil, fmt.Errorf("write provider script: %w", err)
	}
	if err := writeProviderAckKey(dir, files.ackToken); err != nil {
		return nil, fmt.Errorf("write provider acknowledgement key: %w", err)
	}
	return files, nil
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

type providerShellInvocation struct {
	script   string
	force    string
	ack      string
	ackToken string
}

type providerExecution struct {
	cancel    context.CancelCauseFunc
	completed <-chan error
	shell     *providerShellInvocation
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
			return providerCancellationCompletion(context.Cause(parent), process)
		}
		if process.shell != nil {
			return errors.Join(result, providerForceCompletionStatus(process))
		}
		return result
	case <-parent.Done():
	}

	cause := context.Cause(parent)
	if err := publish(marker); err != nil {
		return errors.Join(
			cause,
			fmt.Errorf("publish provider cancellation marker: %w", sanitizeMarkerError(err)),
			forceProviderExecution(cause, process),
		)
	}
	return waitForProviderGrace(cause, process)
}

func waitForProviderGrace(cause error, process providerExecution) error {
	timer := time.NewTimer(providerCancellationGrace)
	defer timer.Stop()
	select {
	case <-process.completed:
		return providerCancellationCompletion(cause, process)
	case <-timer.C:
	}
	return errors.Join(cause, forceProviderExecution(cause, process))
}

func providerCancellationCompletion(cause error, process providerExecution) error {
	if process.shell == nil {
		return cause
	}
	return errors.Join(cause, providerForceCompletionStatus(process))
}

func forceProviderExecution(cause error, process providerExecution) error {
	if process.shell == nil {
		stopProviderExecution(cause, process)
		return nil
	}
	select {
	case <-process.completed:
		return providerForceCompletionStatus(process)
	default:
	}
	if err := publishProviderMarker(process.shell.force); err != nil {
		stopProviderExecution(cause, process)
		return fmt.Errorf("publish provider force marker: %w", err)
	}
	return waitForProviderForceAck(cause, process)
}

func waitForProviderForceAck(cause error, process providerExecution) error {
	timer := time.NewTimer(providerForceAckWait)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-process.completed:
			return providerForceCompletionStatus(process)
		case <-ticker.C:
			acknowledged, err := providerForceAcknowledged(process.shell)
			if err != nil {
				stopProviderExecution(cause, process)
				return fmt.Errorf("inspect provider force acknowledgement: %w", err)
			}
			if acknowledged {
				stopProviderExecution(cause, process)
				return nil
			}
		case <-timer.C:
			return finishProviderForceAtDeadline(cause, process)
		}
	}
}

func providerForceCompletionStatus(process providerExecution) error {
	acknowledged, err := providerForceAcknowledged(process.shell)
	if err != nil {
		return fmt.Errorf("inspect provider force acknowledgement: %w", err)
	}
	if !acknowledged {
		return errors.New("provider helper exited without authentic acknowledgement")
	}
	return nil
}

func finishProviderForceAtDeadline(cause error, process providerExecution) error {
	acknowledged, err := providerForceAcknowledged(process.shell)
	stopProviderExecution(cause, process)
	if err != nil {
		return fmt.Errorf("inspect provider force acknowledgement: %w", err)
	}
	if !acknowledged {
		return errors.New("provider force acknowledgement timed out")
	}
	return nil
}

func stopProviderExecution(cause error, process providerExecution) {
	process.cancel(cause)
	<-process.completed
}

func writeProviderScript(dir, script string) (retErr error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return sanitizeMarkerError(err)
	}
	defer func() { retErr = errors.Join(retErr, sanitizeMarkerError(root.Close())) }()
	f, err := root.OpenFile("script", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return sanitizeMarkerError(err)
	}
	_, writeErr := f.WriteString(script)
	return errors.Join(sanitizeMarkerError(writeErr), sanitizeMarkerError(f.Close()))
}

func writeProviderAckKey(dir, token string) (retErr error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return sanitizeMarkerError(err)
	}
	defer func() { retErr = errors.Join(retErr, sanitizeMarkerError(root.Close())) }()
	f, err := root.OpenFile("ack-key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return sanitizeMarkerError(err)
	}
	_, writeErr := f.WriteString(token)
	return errors.Join(sanitizeMarkerError(writeErr), sanitizeMarkerError(f.Close()))
}

func providerForceAcknowledged(files *providerShellInvocation) (acknowledged bool, retErr error) {
	root, err := os.OpenRoot(filepath.Dir(files.ack))
	if err != nil {
		return false, sanitizeMarkerError(err)
	}
	defer func() { retErr = errors.Join(retErr, sanitizeMarkerError(root.Close())) }()
	return providerForceAckInRoot(root, files.ackToken)
}

func providerForceAckInRoot(root *os.Root, token string) (acknowledged bool, retErr error) {
	info, err := root.Lstat("ack")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, sanitizeMarkerError(err)
	}
	if !privateProviderAck(info, len(token)) {
		return false, nil
	}
	f, err := root.Open("ack")
	if err != nil {
		return false, sanitizeMarkerError(err)
	}
	defer func() { retErr = errors.Join(retErr, sanitizeMarkerError(f.Close())) }()
	return providerForceAckContents(f, token)
}

func providerForceAckContents(f *os.File, token string) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, sanitizeMarkerError(err)
	}
	if !privateProviderAck(info, len(token)) {
		return false, nil
	}
	data := make([]byte, len(token))
	if _, err := io.ReadFull(f, data); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, sanitizeMarkerError(err)
	}
	return subtle.ConstantTimeCompare(data, []byte(token)) == 1, nil
}

func privateProviderAck(info os.FileInfo, length int) bool {
	return info.Mode().IsRegular() && info.Size() == int64(length) &&
		(runtime.GOOS == "windows" || info.Mode().Perm() == 0o600)
}

func providerReservedKey(key string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(key, config.EnvProviderCancelFile)
	}
	return key == config.EnvProviderCancelFile
}

func publishProviderCancellation(marker string) error {
	return publishProviderMarker(marker)
}

func publishProviderMarker(marker string) (retErr error) {
	root, err := os.OpenRoot(filepath.Dir(marker))
	if err != nil {
		return fmt.Errorf("cannot open marker directory: %w", sanitizeMarkerError(err))
	}
	defer func() {
		if err := root.Close(); err != nil {
			retErr = errors.Join(retErr, sanitizeMarkerError(err))
		}
	}()
	name := filepath.Base(marker)
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return sanitizeMarkerError(f.Close())
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("cannot create marker: %w", sanitizeMarkerError(err))
	}
	info, statErr := root.Lstat(name)
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

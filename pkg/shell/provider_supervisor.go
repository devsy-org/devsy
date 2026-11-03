package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const providerWindowsOS = "windows"

type providerProcessSupervisor struct {
	mu       sync.Mutex
	forced   bool
	children map[*exec.Cmd]chan struct{}
}

func (s *providerProcessSupervisor) start(
	ctx context.Context,
	newCommand func() *exec.Cmd,
) (*exec.Cmd, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forced || ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	cmd, err := startProviderExecRetry(ctx, newCommand)
	if err != nil {
		return nil, err
	}
	if s.children == nil {
		s.children = make(map[*exec.Cmd]chan struct{})
	}
	s.children[cmd] = make(chan struct{})
	return cmd, nil
}

func startProviderExecRetry(ctx context.Context, newCommand func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCommand()
	err := cmd.Start()
	for delay := time.Millisecond; errors.Is(err, syscall.ETXTBSY) && delay < 300*time.Millisecond; delay *= 2 {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(delay):
		}
		cmd = newCommand()
		err = cmd.Start()
	}
	if err != nil {
		return nil, err
	}
	return cmd, nil
}

func (s *providerProcessSupervisor) reaped(cmd *exec.Cmd) {
	s.mu.Lock()
	if done, ok := s.children[cmd]; ok {
		delete(s.children, cmd)
		close(done)
	}
	s.mu.Unlock()
}

func (s *providerProcessSupervisor) settled() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, done := range s.children {
		return done
	}
	return nil
}

func (s *providerProcessSupervisor) force(cancel context.CancelFunc) {
	s.mu.Lock()
	s.forced = true
	cancel()
	children := make([]*exec.Cmd, 0, len(s.children))
	for cmd := range s.children {
		children = append(children, cmd)
	}
	s.mu.Unlock()
	for _, cmd := range children {
		_ = cmd.Process.Kill()
	}
	for _, cmd := range children {
		s.mu.Lock()
		done := s.children[cmd]
		s.mu.Unlock()
		if done != nil {
			<-done
		}
	}
}

func providerShellPaths(scriptPath, forcePath, ackPath string) error {
	if !filepath.IsAbs(scriptPath) || filepath.Clean(scriptPath) != scriptPath ||
		filepath.Base(scriptPath) != "script" {
		return errors.New("provider shell requires an absolute private script path")
	}
	dir := filepath.Dir(scriptPath)
	if forcePath != filepath.Join(dir, "force") || ackPath != filepath.Join(dir, "ack") {
		return errors.New("provider shell control files must be private sibling paths")
	}
	return nil
}

func providerShellRoot(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("provider shell directory is inaccessible")
	}
	info, err := root.Stat(".")
	if err != nil || !providerPrivateDirectory(dir, info) {
		_ = root.Close()
		return nil, errors.New("provider shell directory is not private")
	}
	return root, nil
}

func providerShellFiles(root *os.Root) error {
	script, err := root.Lstat("script")
	if err != nil || !script.Mode().IsRegular() || !providerPrivateScript(script) {
		return errors.New("provider shell script is not a private regular file")
	}
	if _, err := providerForcePresentRoot(root); err != nil {
		return err
	}
	_, err = root.Lstat("ack")
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("provider shell acknowledgement already exists or is inaccessible")
	}
	return nil
}

func providerForcePresent(dir string) (bool, error) {
	root, err := providerShellRoot(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	return providerForcePresentRoot(root)
}

func providerForcePresentRoot(root *os.Root) (bool, error) {
	info, err := root.Lstat("force")
	if err == nil {
		if info.Mode().IsRegular() {
			return true, nil
		}
		return false, errors.New("provider shell force marker is not a regular file")
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, errors.New("provider shell force marker is inaccessible")
}

func providerWriteAck(root *os.Root, token string) error {
	file, err := root.OpenFile("ack.tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("create provider shell acknowledgement: " + errStringKind(err))
	}
	defer func() { _ = root.Remove("ack.tmp") }()
	if _, err := file.WriteString(token); err != nil {
		_ = file.Close()
		return errors.New("write provider shell acknowledgement: " + errStringKind(err))
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("sync provider shell acknowledgement: " + errStringKind(err))
	}
	if err := file.Close(); err != nil {
		return errors.New("close provider shell acknowledgement: " + errStringKind(err))
	}
	if err := root.Rename("ack.tmp", "ack"); err != nil {
		return errors.New("publish provider shell acknowledgement: " + errStringKind(err))
	}
	return nil
}

func providerAckToken(root *os.Root) (string, error) {
	invalid := errors.New("invalid provider shell acknowledgement token")
	info, err := root.Lstat("ack-key")
	if err != nil || !providerPrivateAckKey(info) {
		return "", invalid
	}
	token, ok := providerReadAckKey(root)
	if !ok || !validProviderAckToken(token) {
		return "", invalid
	}
	return token, nil
}

func providerPrivateAckKey(info os.FileInfo) bool {
	return info.Mode().IsRegular() &&
		(runtime.GOOS == providerWindowsOS || info.Mode().Perm() == 0o600)
}

func providerReadAckKey(root *os.Root) (string, bool) {
	file, err := root.Open("ack-key")
	if err != nil {
		return "", false
	}
	info, statErr := file.Stat()
	content, readErr := io.ReadAll(io.LimitReader(file, 65))
	closeErr := file.Close()
	removeErr := root.Remove("ack-key")
	if statErr != nil || !providerPrivateAckKey(info) ||
		readErr != nil || closeErr != nil || removeErr != nil {
		return "", false
	}
	return string(content), true
}

func validProviderAckToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for i := range len(token) {
		c := token[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func errStringKind(err error) string {
	if errors.Is(err, os.ErrExist) {
		return "already exists"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission denied"
	}
	return "file operation failed"
}

type providerShellExecution struct {
	ctx        context.Context
	dir        string
	token      string
	root       *os.Root
	result     <-chan error
	supervisor *providerProcessSupervisor
	cancel     context.CancelFunc
}

func RunProviderShell(ctx context.Context, scriptPath, forcePath, ackPath string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err := providerShellPaths(scriptPath, forcePath, ackPath); err != nil {
		return err
	}
	dir := filepath.Dir(scriptPath)
	root, err := providerShellRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	token, err := providerAckToken(root)
	if err != nil {
		return err
	}
	if err := providerShellFiles(root); err != nil {
		return err
	}
	content, err := root.ReadFile("script")
	if err != nil {
		return errors.New("read provider shell script: " + errStringKind(err))
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	supervisor := &providerProcessSupervisor{}
	result := make(chan error, 1)
	go func() {
		result <- RunEmulatedShell(executionCtx, &CommandRunner{
			Command: string(content), Environ: os.Environ(), Stdin: os.Stdin,
			Stdout: os.Stdout, Stderr: os.Stderr, ExecHandler: providerExecHandler(supervisor),
		})
	}()
	execution := providerShellExecution{
		ctx:        ctx,
		dir:        dir,
		root:       root,
		token:      token,
		result:     result,
		supervisor: supervisor,
		cancel:     cancel,
	}
	return execution.wait(ctx)
}

func (p *providerShellExecution) wait(ctx context.Context) error {
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		forced, pollErr := providerForcePresent(p.dir)
		if forced || pollErr != nil || ctx.Err() != nil {
			return p.force(pollErr)
		}
		select {
		case err := <-p.result:
			return p.completed(err)
		case <-poll.C:
		case <-ctx.Done():
		}
	}
}

func (p *providerShellExecution) ackForced() error {
	p.supervisor.force(p.cancel)
	return providerWriteAck(p.root, p.token)
}

func (p *providerShellExecution) force(pollErr error) error {
	if pollErr != nil {
		p.supervisor.force(p.cancel)
		return pollErr
	}
	if err := p.ackForced(); err != nil {
		return err
	}
	<-p.result
	return context.Canceled
}

func (p *providerShellExecution) completed(err error) error {
	forced, pollErr := providerForcePresent(p.dir)
	if pollErr != nil {
		return pollErr
	}
	if forced {
		return p.canceled()
	}
	if earlyErr := providerShellEarlyError(err); earlyErr != nil {
		return earlyErr
	}
	if err := p.waitForChildren(); err != nil {
		return err
	}
	if ackErr := providerWriteAck(p.root, p.token); ackErr != nil {
		return ackErr
	}
	return err
}

func providerShellEarlyError(err error) error {
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), "parse shell command: ") {
		return errors.New("parse provider shell script")
	}
	if strings.HasPrefix(err.Error(), "create shell runner: ") {
		return err
	}
	if pathErr, ok := errors.AsType[*os.PathError](err); ok && pathErr.Op == "getwd" {
		return err
	}
	return nil
}

func (p *providerShellExecution) canceled() error {
	if err := p.ackForced(); err != nil {
		return err
	}
	return context.Canceled
}

func (p *providerShellExecution) waitForChildren() error {
	for done := p.supervisor.settled(); done != nil; done = p.supervisor.settled() {
		select {
		case <-done:
		case <-p.ctx.Done():
			return p.canceled()
		case <-time.After(100 * time.Millisecond):
			forced, pollErr := providerForcePresent(p.dir)
			if pollErr != nil {
				p.supervisor.force(p.cancel)
				return pollErr
			}
			if forced {
				return p.canceled()
			}
		}
	}
	return nil
}

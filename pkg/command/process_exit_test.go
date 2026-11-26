package command

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestProcessExitedRecognizesUnreapedChild(t *testing.T) {
	child := processExitTestChild()
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	identity, err := ProcessTreeIdentity(child.Process.Pid)
	require.NoError(t, err)
	ref := ProcessRef{PID: child.Process.Pid, Identity: identity}
	exited, err := ProcessExited(ref)
	require.NoError(t, err)
	require.False(t, exited)
	require.NoError(t, child.Process.Kill())
	require.Eventually(t, func() bool {
		exited, err := ProcessExited(ref)
		return err == nil && exited
	}, time.Second, time.Millisecond, "exit must be observable before the parent reaps the child")
}

func TestProcessExitedRequiresSavedWorkerIdentity(t *testing.T) {
	child := processExitTestChild()
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill() })
	identity, err := ProcessTreeIdentity(child.Process.Pid)
	require.NoError(t, err)
	ref := ProcessRef{PID: child.Process.Pid, Identity: identity}
	exited, err := ProcessExited(ref)
	require.NoError(t, err)
	require.False(t, exited)
	stale := ref
	stale.Identity += "-old"
	exited, err = ProcessExited(stale)
	require.NoError(t, err)
	require.True(t, exited)
	// A stale identity describes an exited worker, never authority to signal the current PID.
	exited, err = ProcessExited(ref)
	require.NoError(t, err)
	require.False(t, exited)
	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	exited, err = ProcessExited(ref)
	require.NoError(t, err)
	require.True(t, exited)
	ref.Identity = ""
	exited, err = ProcessExited(ref)
	require.NoError(t, err)
	require.False(t, exited)
}

func TestSupervisedPublicationFailureCleansUnclaimedWorker(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	child := processExitTestChild()
	var ref ProcessRef
	publicationErr := errors.New("publication rejected")
	err := startSupervisedDetached(child, SupervisedStartOptions{
		Name: "test-publication-failure",
		OnStarted: func(started ProcessRef) error {
			ref = started
			exited, err := ProcessExited(ref)
			require.NoError(t, err)
			require.False(t, exited, "a published child waiting to resume is healthy")
			return publicationErr
		},
	}, filepath.Join(t.TempDir(), "streams"))
	require.ErrorIs(t, err, publicationErr)
	require.Positive(t, ref.PID)
	exited, err := ProcessExited(ref)
	require.NoError(t, err)
	require.True(t, exited)
}

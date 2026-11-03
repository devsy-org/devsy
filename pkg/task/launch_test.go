package task

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestReconcileLaunchAbandonedBeforeSpawn(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, tk.BeginLaunch())
	require.NoError(t, releaseLauncherLock(tk))
	state, err := store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.Equal(t, StatusFailed, state.Status)
	require.False(t, state.LaunchPending)
	require.True(t, state.ProcessCleanupComplete)
}

func TestLaunchCommitRequiresReadinessAndPreservesCancellation(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.CreateLaunch(CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, releaseLauncherLock(tk)) })
	identity, err := command.ProcessTreeIdentity(os.Getpid())
	require.NoError(t, err)
	ref := command.ProcessRef{PID: os.Getpid(), Identity: identity}
	require.NoError(t, tk.PublishProcess(ref))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, tk.CommitLaunch(ctx), context.Canceled)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.LaunchPending)
	require.ErrorIs(t, tk.WorkerLaunchReady(os.Getpid()+1), ErrCanceled)
	require.NoError(t, tk.WorkerLaunchReady(os.Getpid()))
	_, err = tk.requestCancel()
	require.NoError(t, err)
	require.ErrorIs(t, tk.CommitLaunch(context.Background()), ErrCanceled)
	state, err = store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.LaunchPending)
	require.True(t, state.CancelRequested)
	require.NoError(t, releaseLauncherLock(tk))
	store.processController = fakeProcessController{}
	state, err = store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.True(t, state.Canceled())
	require.True(t, state.ProcessCleanupComplete)
}

func TestLegacyPendingLaunchCannotClaimCleanup(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, store.update(tk.ID(), func(s *State) { s.LaunchPending = true }))
	state, err := store.ReconcileTask(context.Background(), tk.ID())
	require.ErrorContains(t, err, "legacy abandoned launch")
	require.True(t, state.LaunchPending)
	require.False(t, state.ProcessCleanupComplete)
	require.Error(t, tk.Cancel())
	state, err = store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.LaunchPending)
	require.False(t, state.ProcessCleanupComplete)
}

func TestFailedLaunchAfterCommitStillCleansTheWorkerTree(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.CreateLaunch(CreateOptions{})
	require.NoError(t, err)
	identity, err := command.ProcessTreeIdentity(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, tk.PublishProcess(command.ProcessRef{PID: os.Getpid(), Identity: identity}))
	require.NoError(t, tk.WorkerLaunchReady(os.Getpid()))
	require.NoError(t, tk.CommitLaunch(context.Background()))
	terminationCalls := 0
	store.processController = fakeProcessController{
		terminate: func(command.ProcessRef) error { terminationCalls++; return nil },
	}
	failure := errors.New("launch handoff could not release ownership")
	require.NoError(t, tk.FailLaunch(failure))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, 1, terminationCalls)
	require.True(t, state.ProcessCleanupComplete)
	require.False(t, state.LaunchPending)
	require.Equal(t, StatusFailed, state.Status)
	require.Contains(t, state.Error, failure.Error())
	require.NoError(t, tk.FailLaunch(failure))
	require.Equal(
		t,
		1,
		terminationCalls,
		"verified cleanup must not signal the saved reference again",
	)
}

func TestCommittedLaunchCleanupFailureCanBeRetried(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.CreateLaunch(CreateOptions{})
	require.NoError(t, err)
	identity, err := command.ProcessTreeIdentity(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, tk.PublishProcess(command.ProcessRef{PID: os.Getpid(), Identity: identity}))
	require.NoError(t, tk.WorkerLaunchReady(os.Getpid()))
	require.NoError(t, tk.CommitLaunch(context.Background()))
	cleanupFailure := errors.New("process cleanup unavailable")
	store.processController = fakeProcessController{
		terminate: func(command.ProcessRef) error { return cleanupFailure },
	}
	require.ErrorIs(t, tk.FailLaunch(errors.New("handoff failed")), cleanupFailure)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.LaunchPending)
	require.False(t, state.ProcessCleanupComplete)
	require.Error(t, store.Delete(tk.ID(), true))
	store.processController = fakeProcessController{}
	state, err = store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.False(t, state.LaunchPending)
	require.True(t, state.ProcessCleanupComplete)
	require.Contains(t, state.Error, "handoff failed")
}

func TestAbandonedLaunchCleanupFailureRemainsRetryable(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.CreateLaunch(CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, tk.PublishProcess(command.ProcessRef{PID: 123, Identity: "fixture"}))
	require.NoError(t, releaseLauncherLock(tk))
	cleanupFailure := errors.New("cannot verify cleanup")
	store.processController = fakeProcessController{
		terminate: func(command.ProcessRef) error { return cleanupFailure },
	}
	state, err := store.ReconcileTask(context.Background(), tk.ID())
	require.ErrorIs(t, err, cleanupFailure)
	require.True(t, state.LaunchPending)
	require.False(t, state.ProcessCleanupComplete)
	require.False(t, state.Status.Terminal())
	require.Error(t, store.Delete(tk.ID(), true))
	store.processController = fakeProcessController{}
	state, err = store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.False(t, state.LaunchPending)
	require.True(t, state.ProcessCleanupComplete)
	require.Equal(t, StatusFailed, state.Status)
}

func TestLauncherCrashCannotAuthorizeWorkspaceWork(t *testing.T) {
	for _, stage := range []string{"before-spawn", "before-publication", "after-publication", "after-ready"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			launcher := exec.Command(
				os.Args[0],
				"-test.run=^TestLaunchCrashHelper$",
			) // #nosec G204 G702 -- re-exec this test binary
			launcher.Env = append(
				os.Environ(),
				"DEVSY_LAUNCH_TEST=launcher",
				"DEVSY_LAUNCH_STAGE="+stage,
				config.EnvHome+"="+dir,
				"DEVSY_LAUNCH_STORE="+filepath.Join(dir, "tasks"),
			)
			stdout, err := launcher.StdoutPipe()
			require.NoError(t, err)
			stdin, err := launcher.StdinPipe()
			require.NoError(t, err)
			launcher.Stderr = os.Stderr
			require.NoError(t, launcher.Start())
			t.Cleanup(func() { _ = stdin.Close(); _ = launcher.Process.Kill() })
			line := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				if scanner.Scan() {
					line <- scanner.Text()
				} else {
					line <- ""
				}
			}()
			var id string
			select {
			case id = <-line:
				require.Len(t, id, 12)
			case <-time.After(15 * time.Second):
				t.Fatal("launcher did not reach crash barrier")
			}
			store, err := NewStoreAt(filepath.Join(dir, "tasks"))
			require.NoError(t, err)
			state, err := store.ReconcileTask(context.Background(), id)
			require.NoError(t, err)
			require.True(t, state.LaunchPending, "a live launcher still owns the handoff")
			require.NoError(t, launcher.Process.Kill())
			require.Error(t, launcher.Wait())
			require.Eventually(t, func() bool {
				state, err = store.ReconcileTask(context.Background(), id)
				return err == nil && !state.LaunchPending && state.ProcessCleanupComplete
			}, 15*time.Second, 20*time.Millisecond, "abandoned launch must reconcile with proven cleanup")
			require.Equal(t, StatusFailed, state.Status)
			_, err = os.Stat(filepath.Join(dir, "workspace-work-authorized"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestLaunchCrashHelper(t *testing.T) {
	role := os.Getenv("DEVSY_LAUNCH_TEST")
	if role == "" {
		return
	}
	config.ResetPathManager()
	store, err := NewStoreAt(os.Getenv("DEVSY_LAUNCH_STORE"))
	require.NoError(t, err)
	if role == "worker" {
		runLaunchCrashWorker(t, store)
		return
	}
	tk, err := store.CreateLaunch(CreateOptions{})
	require.NoError(t, err)
	barrier := func() {
		_, err := fmt.Fprintln(os.Stdout, tk.ID())
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	stage := os.Getenv("DEVSY_LAUNCH_STAGE")
	if stage == "before-spawn" {
		barrier()
		return
	}
	err = command.StartSupervisedBackground(command.SupervisedStartOptions{
		Name: WorkerProcessName(
			tk.ID(),
		),
		Lease:           tk.LaunchLease(),
		AtomicOwnership: true,
		OnPrepared:      tk.PrepareProcessOwnership,
		OnStarted: func(ref command.ProcessRef) error {
			if stage != "before-publication" {
				require.NoError(t, tk.PublishProcess(ref))
			}
			if stage == "after-ready" {
				require.Eventually(
					t,
					func() bool { state, err := store.Get(tk.ID()); return err == nil && state.WorkerReady },
					5*time.Second,
					20*time.Millisecond,
				)
			}
			barrier()
			return fmt.Errorf("test barrier released without commit")
		},
	}, func() (*exec.Cmd, error) {
		worker := exec.Command(
			os.Args[0],
			"-test.run=^TestLaunchCrashHelper$",
		) // #nosec G204 G702 -- re-exec this test binary
		worker.Env = append(os.Environ(), "DEVSY_LAUNCH_TEST=worker", "DEVSY_LAUNCH_ID="+tk.ID())
		return worker, nil
	})
	require.Error(t, err)
}

func runLaunchCrashWorker(t *testing.T, store *Store) {
	tk := store.Open(os.Getenv("DEVSY_LAUNCH_ID"))
	require.NoError(t, command.RetainLaunchLease())
	require.NoError(t, tk.HoldWorkerLock())
	defer func() { require.NoError(t, tk.ReleaseWorkerLock()) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		state, err := store.Get(tk.ID())
		require.NoError(t, err)
		if !advanceCrashWorker(t, tk, state) {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("worker bootstrap did not resolve")
		}
	}
}

func advanceCrashWorker(t *testing.T, tk *Task, state *State) bool {
	if state.Status.Terminal() || state.CancelRequested {
		return false
	}
	if !state.LaunchPending {
		require.NoError(
			t,
			// #nosec G703 -- the parent test supplies its private TempDir as DEVSY_HOME.
			os.WriteFile(
				filepath.Join(os.Getenv(config.EnvHome), "workspace-work-authorized"),
				nil,
				0o600,
			),
		)
		return false
	}
	alive, err := tk.LauncherAlive()
	require.NoError(t, err)
	if !alive {
		return false
	}
	if state.PID != 0 {
		require.NoError(t, tk.WorkerLaunchReady(os.Getpid()))
	}
	return true
}

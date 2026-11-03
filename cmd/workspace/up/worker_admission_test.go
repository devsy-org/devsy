package up

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

type admissionReferenceKind int

const (
	admissionNoReference admissionReferenceKind = iota
	admissionLiveReference
	admissionExitedReference
	admissionStaleReference
	admissionWeakReference
)

func TestDetachedAdmissionRejectsUnverifiedWorker(t *testing.T) {
	cases := []struct {
		name      string
		reference admissionReferenceKind
		protocol  int
		pending   bool
		canceled  bool
	}{
		{name: "live-legacy", reference: admissionLiveReference},
		{name: "exited-legacy", reference: admissionExitedReference},
		{name: "committed-protocol", reference: admissionLiveReference, protocol: 1},
		{name: "stale-identity", reference: admissionStaleReference},
		{name: "weak-identity", reference: admissionWeakReference},
		{name: "missing-reference", reference: admissionNoReference},
		{name: "pending-mismatch", reference: admissionLiveReference, protocol: 1, pending: true},
		{name: "unpublished", reference: admissionNoReference, protocol: 1, pending: true},
		{name: "canceled-mismatch", reference: admissionLiveReference, canceled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := isolatedTaskStore(t)
			tk, err := store.Create(task.CreateOptions{Command: "up"})
			require.NoError(t, err)
			state, err := store.Get(tk.ID())
			require.NoError(t, err)
			state.Process = detachedAdmissionReference(t, tc.reference)
			if state.Process != nil {
				state.PID = state.Process.PID
				state.ProcessTreeIdentity = state.Process.Identity
			}
			state.LaunchProtocol, state.LaunchPending = tc.protocol, tc.pending
			state.CancelRequested = tc.canceled
			writeDetachedAdmissionState(t, state)
			admitted, err := (&UpCmd{taskID: tk.ID()}).openTask()
			if admitted != nil {
				require.NoError(t, admitted.ReleaseWorkerLock())
			}
			require.Error(t, err)
			require.Nil(t, admitted)
			after, err := store.Get(tk.ID())
			require.NoError(t, err)
			require.Equal(t, state, after, "unverified invocation must not mutate the saved owner")
			assertWorkerLockReleased(t, store, tk.ID())
		})
	}
}

func detachedAdmissionReference(t *testing.T, kind admissionReferenceKind) *command.ProcessRef {
	t.Helper()
	if kind == admissionNoReference {
		return nil
	}
	if kind == admissionLiveReference || kind == admissionExitedReference {
		ref := detachedAdmissionChild(t, kind == admissionExitedReference)
		return &ref
	}
	identity, err := command.ProcessTreeIdentity(os.Getpid())
	require.NoError(t, err)
	if kind == admissionStaleReference {
		identity += "-stale"
	} else {
		identity = ""
	}
	return &command.ProcessRef{PID: os.Getpid(), Identity: identity}
}

func TestDetachedAdmissionAcceptsCurrentIdentity(t *testing.T) {
	for _, protocol := range []int{0, 1} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			store := isolatedTaskStore(t)
			tk, err := store.Create(task.CreateOptions{Command: "up"})
			require.NoError(t, err)
			require.NoError(t, tk.SetPID(os.Getpid()))
			state, err := store.Get(tk.ID())
			require.NoError(t, err)
			state.LaunchProtocol = protocol
			writeDetachedAdmissionState(t, state)
			admitted, err := (&UpCmd{taskID: tk.ID()}).openTask()
			require.NoError(t, err)
			require.NotNil(t, admitted)
			require.NoError(t, admitted.ReleaseWorkerLock())
		})
	}
}

func writeDetachedAdmissionState(t *testing.T, state *task.State) {
	t.Helper()
	dir, err := config.DefaultPathManager().TaskDir()
	require.NoError(t, err)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, root.Close()) }()
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, root.WriteFile(state.ID+".json", data, 0o600))
}

func detachedAdmissionChild(t *testing.T, exited bool) command.ProcessRef {
	t.Helper()
	child := exec.Command(
		os.Args[0], "-test.run=^TestDetachedAdmissionChild$",
	) // #nosec G204 G702 -- re-exec this test binary with a fixed helper selector
	child.Env = append(os.Environ(), "DEVSY_TEST_ADMISSION_CHILD=1")
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	identity, err := command.ProcessTreeIdentity(child.Process.Pid)
	require.NoError(t, err)
	require.NotEmpty(t, identity)
	ref := command.ProcessRef{PID: child.Process.Pid, Identity: identity}
	if exited {
		require.NoError(t, child.Process.Kill())
		_ = child.Wait()
		gone, err := command.ProcessExited(ref)
		require.NoError(t, err)
		require.True(t, gone)
	}
	return ref
}

func TestDetachedAdmissionChild(t *testing.T) {
	if os.Getenv("DEVSY_TEST_ADMISSION_CHILD") != "1" {
		return
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	require.NoError(t, err)
}

func TestDetachedAdmissionOwnedBootstrapErrorPersists(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.SetPID(os.Getpid()))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	state.LaunchProtocol, state.LaunchPending = 1, true
	writeDetachedAdmissionState(t, state)
	admitted, err := (&UpCmd{taskID: tk.ID()}).openTask()
	require.Error(t, err)
	require.Nil(t, admitted)
	after, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, after.Status)
	require.Equal(t, state.Process, after.Process)
	require.True(t, after.LaunchPending)
	require.False(t, after.WorkerReady)
	assertWorkerLockReleased(t, store, tk.ID())
}

func TestDetachedAdmissionKeepsLegacyPendingUnverified(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.SetPID(os.Getpid()))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	state.LaunchPending = true
	writeDetachedAdmissionState(t, state)
	progress := workerLaunchProgress{}
	ready, err := advanceTaskLaunch(tk, store, state, &progress)
	require.NoError(t, err)
	require.False(t, ready)
	require.False(t, progress.verifiedOwner)
	require.False(t, progress.leaseRetained)
	after, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, state, after)
}

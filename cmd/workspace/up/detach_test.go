package up

import (
	"errors"
	"reflect"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
)

const (
	testRepoArg   = "myrepo"
	testDebugFlag = "--debug"
)

func TestDetachedArgs_StripsDetachFlag(t *testing.T) {
	got := detachedArgs([]string{testRepoArg, "--detach", testDebugFlag})
	want := []string{testRepoArg, testDebugFlag}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

func TestDetachedArgs_StripsDetachEqualsValue(t *testing.T) {
	got := detachedArgs([]string{testRepoArg, "--detach=true", testDebugFlag})
	want := []string{testRepoArg, testDebugFlag}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

func TestDetachedArgs_NoDetachFlagIsUnchanged(t *testing.T) {
	got := detachedArgs([]string{testRepoArg, testDebugFlag})
	want := []string{testRepoArg, testDebugFlag}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

func TestDetachWorkspaceLabelNormalizesPath(t *testing.T) {
	cmd := &UpCmd{}
	raw := "/tmp/some workspace dir"
	got := cmd.detachWorkspaceLabel([]string{raw})
	want := workspace2.ToID(raw)
	if got != want {
		t.Errorf("detachWorkspaceLabel() = %q, want %q", got, want)
	}
	if got == raw {
		t.Error("label must be the workspace ID, not the raw path")
	}
}

func TestOpenTaskExitsWhenAlreadyCanceled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	store, err := task.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tk.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	cmd := &UpCmd{taskID: tk.ID()}
	got, err := cmd.openTask()
	if got != nil || !errors.Is(err, task.ErrCanceled) {
		t.Fatalf("openTask() = (%v, %v), want (nil, ErrCanceled)", got, err)
	}

	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Error != task.ErrCanceled.Error() {
		t.Errorf("openTask overwrote the canceled state: %+v", state)
	}
}

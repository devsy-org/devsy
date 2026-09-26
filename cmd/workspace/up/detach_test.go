package up

import (
	"reflect"
	"testing"

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

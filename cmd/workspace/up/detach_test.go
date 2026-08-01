package up

import (
	"reflect"
	"testing"
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

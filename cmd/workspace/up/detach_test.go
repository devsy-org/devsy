package up

import (
	"reflect"
	"testing"
)

func TestDetachedArgs_StripsDetachFlag(t *testing.T) {
	got := detachedArgs([]string{"myrepo", "--detach", "--debug"})
	want := []string{"myrepo", "--debug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

func TestDetachedArgs_StripsDetachEqualsValue(t *testing.T) {
	got := detachedArgs([]string{"myrepo", "--detach=true", "--debug"})
	want := []string{"myrepo", "--debug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

func TestDetachedArgs_NoDetachFlagIsUnchanged(t *testing.T) {
	got := detachedArgs([]string{"myrepo", "--debug"})
	want := []string{"myrepo", "--debug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detachedArgs() = %v, want %v", got, want)
	}
}

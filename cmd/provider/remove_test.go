package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestRemoveCmd_RejectsMultipleArgs(t *testing.T) {
	globalFlags := &flags.GlobalFlags{}
	cmd := NewRemoveCmd(globalFlags)
	err := cmd.Args(cmd, []string{"provider1", "provider2"})
	if err == nil {
		t.Fatal("expected error when passing multiple arguments to remove, got nil")
	}
}

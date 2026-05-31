package provider

import (
	"slices"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestDeleteCmd_RejectsMultipleArgs(t *testing.T) {
	globalFlags := &flags.GlobalFlags{}
	cmd := NewDeleteCmd(globalFlags)
	err := cmd.Args(cmd, []string{"provider1", "provider2"})
	if err == nil {
		t.Fatal("expected error when passing multiple arguments to delete, got nil")
	}
}

func TestDeleteCmdHasRmAlias(t *testing.T) {
	cmd := NewDeleteCmd(&flags.GlobalFlags{})
	found := slices.Contains(cmd.Aliases, "rm")
	if !found {
		t.Fatal("provider delete should expose 'rm' alias")
	}
}

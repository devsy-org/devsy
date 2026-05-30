package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestNewVersionsCmd_Wiring(t *testing.T) {
	cmd := NewVersionsCmd(&flags.GlobalFlags{})
	if cmd.Use != "versions [name]" {
		t.Errorf("Use: got %q want %q", cmd.Use, "versions [name]")
	}
	for _, name := range []string{"json", "prerelease", "no-cache"} {
		if cmd.Flag(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
	// MaximumNArgs(1): 0 and 1 ok, 2 not.
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("0 args should be ok, got %v", err)
	}
	if err := cmd.Args(cmd, []string{testProviderFoo}); err != nil {
		t.Errorf("1 arg should be ok, got %v", err)
	}
	if err := cmd.Args(cmd, []string{testProviderFoo, testProviderBar}); err == nil {
		t.Error("2 args should fail")
	}
}

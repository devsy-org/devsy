package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestNewDefaultCmd_Wiring(t *testing.T) {
	cmd := NewDefaultCmd(&flags.GlobalFlags{})
	if cmd.Use != "default <name>" {
		t.Errorf("Use: got %q want %q", cmd.Use, "default <name>")
	}
	if cmd.Short == "" {
		t.Error("Short must be set")
	}
	// ExactArgs(1): passing 0 args must fail, passing 1 must accept (no Run here so just check Args fn).
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error for 0 args")
	}
	if err := cmd.Args(cmd, []string{"foo"}); err != nil {
		t.Errorf("expected no error for 1 arg, got %v", err)
	}
	if err := cmd.Args(cmd, []string{"foo", "bar"}); err == nil {
		t.Error("expected error for 2 args")
	}
}

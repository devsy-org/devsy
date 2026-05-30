package provider

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

func TestNewConfigureCmd(t *testing.T) {
	cmd := NewConfigureCmd(&flags.GlobalFlags{})
	if cmd.Use != "configure [name]" {
		t.Errorf("Use: got %q want %q", cmd.Use, "configure [name]")
	}
	if cmd.Short == "" {
		t.Error("Short must be set")
	}
	// Verify flags exist
	for _, flag := range []string{"reconfigure", "single-machine", "option", "skip-init"} {
		if cmd.Flag(flag) == nil {
			t.Errorf("missing flag %q", flag)
		}
	}
	// Verify skip-init is hidden
	if !cmd.Flag("skip-init").Hidden {
		t.Error("skip-init must be hidden")
	}
}

func TestResolveProviderName(t *testing.T) {
	if got, err := resolveProviderName([]string{"foo"}, "fallback"); err != nil || got != "foo" {
		t.Fatalf("explicit arg should win: got %q err %v", got, err)
	}
	if got, err := resolveProviderName([]string{}, "fallback"); err != nil || got != "fallback" {
		t.Fatalf("fallback should be used: got %q err %v", got, err)
	}
	if _, err := resolveProviderName([]string{}, ""); err == nil {
		t.Fatal("empty args + empty fallback must error")
	}
}

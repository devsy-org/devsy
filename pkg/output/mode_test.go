package output

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/terminal"
)

func TestResolveMode_JSON(t *testing.T) {
	got := ResolveMode("json")
	if got != ModeJSON {
		t.Errorf("ResolveMode(\"json\") = %q, want %q", got, ModeJSON)
	}
}

func TestResolveMode_Plain(t *testing.T) {
	got := ResolveMode("plain")
	if got != ModePlain {
		t.Errorf("ResolveMode(\"plain\") = %q, want %q", got, ModePlain)
	}
}

func TestResolveMode_Auto_NonTTY(t *testing.T) {
	orig := terminal.IsTerminalOut
	terminal.IsTerminalOut = false
	defer func() { terminal.IsTerminalOut = orig }()

	got := ResolveMode("auto")
	if got != ModeJSON {
		t.Errorf("ResolveMode(\"auto\") with non-TTY = %q, want %q", got, ModeJSON)
	}
}

func TestResolveMode_Auto_TTY(t *testing.T) {
	orig := terminal.IsTerminalOut
	terminal.IsTerminalOut = true
	defer func() { terminal.IsTerminalOut = orig }()

	got := ResolveMode("auto")
	if got != ModePlain {
		t.Errorf("ResolveMode(\"auto\") with TTY = %q, want %q", got, ModePlain)
	}
}

package output

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/terminal"
)

func TestResolveMode_JSON(t *testing.T) {
	got, err := ResolveMode("json")
	if err != nil {
		t.Fatalf("ResolveMode(\"json\") returned error: %v", err)
	}
	if got != ModeJSON {
		t.Errorf("ResolveMode(\"json\") = %q, want %q", got, ModeJSON)
	}
}

func TestResolveMode_Plain(t *testing.T) {
	got, err := ResolveMode("plain")
	if err != nil {
		t.Fatalf("ResolveMode(\"plain\") returned error: %v", err)
	}
	if got != ModePlain {
		t.Errorf("ResolveMode(\"plain\") = %q, want %q", got, ModePlain)
	}
}

func TestResolveMode_Auto_NonTTY(t *testing.T) {
	orig := terminal.IsTerminalOut
	terminal.IsTerminalOut = false
	defer func() { terminal.IsTerminalOut = orig }()

	got, err := ResolveMode("auto")
	if err != nil {
		t.Fatalf("ResolveMode(\"auto\") returned error: %v", err)
	}
	if got != ModeJSON {
		t.Errorf("ResolveMode(\"auto\") with non-TTY = %q, want %q", got, ModeJSON)
	}
}

func TestResolveMode_Auto_TTY(t *testing.T) {
	orig := terminal.IsTerminalOut
	terminal.IsTerminalOut = true
	defer func() { terminal.IsTerminalOut = orig }()

	got, err := ResolveMode("auto")
	if err != nil {
		t.Fatalf("ResolveMode(\"auto\") returned error: %v", err)
	}
	if got != ModePlain {
		t.Errorf("ResolveMode(\"auto\") with TTY = %q, want %q", got, ModePlain)
	}
}

func TestResolveMode_InvalidValue(t *testing.T) {
	got, err := ResolveMode("bogus")
	if err == nil {
		t.Fatalf("ResolveMode(\"bogus\") expected error, got nil (value=%q)", got)
	}
	if got != "" {
		t.Errorf("ResolveMode(\"bogus\") = %q, want empty string", got)
	}
}

package output

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/terminal"
)

func TestResolveMode_DesktopHonorsExplicitFormat(t *testing.T) {
	t.Setenv("DEVSY_UI", "true")
	for value, want := range map[string]string{"auto": ModeJSON, "plain": ModePlain, "text": ModePlain, "json": ModeJSON} {
		got, err := ResolveMode(value)
		if err != nil {
			t.Fatalf("ResolveMode(%q) returned error: %v", value, err)
		}
		if got != want {
			t.Errorf("ResolveMode(%q) = %q, want %q", value, got, want)
		}
	}
}

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
	const value = "private-result-format-canary-82496"
	got, err := ResolveMode(value)
	if err == nil {
		t.Fatalf("expected invalid result format error, got nil (value=%q)", got)
	}
	if strings.Contains(err.Error(), value) || !strings.Contains(err.Error(), "plain") {
		t.Fatalf("invalid result format diagnostic = %q", err)
	}
	if got != "" {
		t.Errorf("ResolveMode returned %q, want empty string", got)
	}
}

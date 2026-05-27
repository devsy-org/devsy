package opener

import (
	"strings"
	"testing"
)

func TestIDELaunchMode_Set_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  IDELaunchMode
	}{
		{"auto", LaunchAuto},
		{"headless", LaunchHeadless},
		{"skip", LaunchSkip},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var m IDELaunchMode
			if err := m.Set(tt.input); err != nil {
				t.Fatalf("Set(%q) returned error: %v", tt.input, err)
			}
			if m != tt.want {
				t.Errorf("after Set(%q), m = %q, want %q", tt.input, m, tt.want)
			}
			if got := m.String(); got != tt.input {
				t.Errorf("String() = %q, want %q", got, tt.input)
			}
		})
	}
}

func TestIDELaunchMode_Set_Invalid(t *testing.T) {
	tests := []string{
		"HEADLESS", // case-strict
		"",
		"yes",
		"true",
		"none",
		"Auto",
		"Skip",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			var m IDELaunchMode
			err := m.Set(in)
			if err == nil {
				t.Fatalf("Set(%q) returned nil error; want error", in)
			}
			if !strings.Contains(err.Error(), "must be one of") {
				t.Errorf("error %q does not contain %q", err.Error(), "must be one of")
			}
		})
	}
}

func TestIDELaunchMode_Type(t *testing.T) {
	var m IDELaunchMode
	if got := m.Type(); got != "auto|headless|skip" {
		t.Errorf("Type() = %q, want %q", got, "auto|headless|skip")
	}
}

func TestIDELaunchMode_String_ZeroValue(t *testing.T) {
	var m IDELaunchMode
	if got := m.String(); got != "auto" {
		t.Errorf("zero-value String() = %q, want %q", got, "auto")
	}
}

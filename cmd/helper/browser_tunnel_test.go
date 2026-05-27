package helper

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
)

// TestNewBrowserTunnelCmd_OpenBrowserFlag locks down the flag plumbing for
// --open-browser. Without this wiring the helper has no way to know it
// should launch a host browser, since the parent CLI no longer owns that.
func TestNewBrowserTunnelCmd_OpenBrowserFlag(t *testing.T) {
	gf := &flags.GlobalFlags{}
	c := NewBrowserTunnelCmd(gf)
	if err := c.ParseFlags([]string{
		"--workspace", "ws",
		"--target-url", "http://localhost:1234",
		"--open-browser",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	openFlag := c.Flags().Lookup("open-browser")
	if openFlag == nil {
		t.Fatal("--open-browser flag not registered")
	}
	if openFlag.Value.String() != "true" {
		t.Errorf("--open-browser = %q, want true", openFlag.Value.String())
	}

	// Default (flag absent) is false.
	gf2 := &flags.GlobalFlags{}
	c2 := NewBrowserTunnelCmd(gf2)
	if err := c2.ParseFlags([]string{"--workspace", "ws"}); err != nil {
		t.Fatalf("ParseFlags (default): %v", err)
	}
	if c2.Flags().Lookup("open-browser").Value.String() != "false" {
		t.Errorf("--open-browser default = %q, want false",
			c2.Flags().Lookup("open-browser").Value.String())
	}
}

func TestParseInheritedListenerEntry_Errors(t *testing.T) {
	tests := []struct {
		name      string
		entry     string
		wantInErr string
	}{
		{
			name:      "no equals sign",
			entry:     "no-equals-sign",
			wantInErr: "expected host:port=fd",
		},
		{
			name:      "non-numeric fd",
			entry:     "localhost:10800=notanumber",
			wantInErr: "invalid fd",
		},
		{
			name:      "negative fd",
			entry:     "localhost:10800=-1",
			wantInErr: "must be >= 3",
		},
		{
			name:      "stdio fd (must be >= 3)",
			entry:     "localhost:10800=2",
			wantInErr: "must be >= 3",
		},
		{
			name:      "empty host",
			entry:     "=3",
			wantInErr: "empty host:port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, l, err := parseInheritedListenerEntry(tt.entry)
			if err == nil {
				if l != nil {
					_ = l.Close()
				}
				t.Fatalf("expected error containing %q, got nil (host=%q)", tt.wantInErr, host)
			}
			if l != nil {
				_ = l.Close()
				t.Errorf("expected nil listener on error, got non-nil")
			}
			if host != "" {
				t.Errorf("expected empty host on error, got %q", host)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

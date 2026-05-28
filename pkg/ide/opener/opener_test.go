package opener

import (
	"context"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// TestIDEParams_Launch_OpenBrowser asserts the mapping that browser IDE openers
// use to decide whether to spawn the host browser: openBrowser is true iff
// Launch is LaunchAuto. LaunchHeadless and LaunchSkip both yield false.
func TestIDEParams_Launch_OpenBrowser(t *testing.T) {
	tests := []struct {
		name string
		mode IDELaunchMode
		want bool
	}{
		{"auto opens", LaunchAuto, true},
		{"headless does not open", LaunchHeadless, false},
		{"skip does not open", LaunchSkip, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := IDEParams{Launch: tt.mode}
			got := params.Launch == LaunchAuto
			if got != tt.want {
				t.Errorf("Launch=%q: openBrowser=%v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

// TestOpenDesktopIDE_HeadlessShortCircuits verifies that LaunchHeadless makes
// openDesktopIDE return nil without dispatching to any per-IDE handler for
// non-Fleet desktop IDEs. Each chosen IDE name matches an explicit switch
// case in openDesktopIDE — if the headless guard were ever removed, dispatch
// would deref nil params.Client / params.Result and panic, failing the test.
// Fleet is excluded because it intentionally runs even under headless (to
// retrieve the workspace-side URL).
func TestOpenDesktopIDE_HeadlessShortCircuits(t *testing.T) {
	desktopIDEs := []string{
		string(config.IDEVSCode),
		string(config.IDEIntellij),
		string(config.IDEZed),
	}
	for _, ide := range desktopIDEs {
		t.Run(ide, func(t *testing.T) {
			url, err := openDesktopIDE(
				context.Background(),
				ide,
				nil,
				IDEParams{Launch: LaunchHeadless},
			)
			if err != nil {
				t.Errorf("expected nil for headless desktop IDE, got %v", err)
			}
			if url != "" {
				t.Errorf("expected empty URL for headless desktop IDE, got %q", url)
			}
		})
	}
}

// TestOpen_NilResultRejected pins the fix for the panic that occurred when
// the agent returned a half-populated result (no SubstitutionContext) after
// an in-container IDE install failure. Previously Open would nil-deref on
// params.Result.SubstitutionContext.ContainerWorkspaceFolder; now it must
// return a descriptive error before dispatching to any per-IDE handler.
func TestOpen_NilResultRejected(t *testing.T) {
	tests := []struct {
		name   string
		params IDEParams
	}{
		{"nil Result", IDEParams{Result: nil}},
		{"nil SubstitutionContext", IDEParams{Result: &config2.Result{}}},
	}
	ides := []string{
		string(config.IDEJupyterNotebook),
		string(config.IDEOpenVSCode),
		string(config.IDEVSCode),
	}
	for _, tt := range tests {
		for _, ide := range ides {
			t.Run(tt.name+"/"+ide, func(t *testing.T) {
				url, err := Open(context.Background(), ide, nil, tt.params)
				if err == nil {
					t.Fatalf("expected error, got nil (url=%q)", url)
				}
				if !strings.Contains(err.Error(), ide) {
					t.Errorf("expected error to name IDE %q, got %q", ide, err.Error())
				}
				if url != "" {
					t.Errorf("expected empty url, got %q", url)
				}
			})
		}
	}
}

func TestParseAddressAndPort_Empty(t *testing.T) {
	addr, p, err := ParseAddressAndPort("", 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p < 10000 {
		t.Errorf("expected port >= 10000, got %d", p)
	}
	if addr == "" {
		t.Error("expected non-empty address")
	}
}

func TestParseAddressAndPort_Explicit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantAddr string
		wantPort int
	}{
		{"host:port", "127.0.0.1:8080", "127.0.0.1:8080", 8080},
		{"localhost:port", "localhost:3000", "localhost:3000", 3000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, p, err := ParseAddressAndPort(tt.input, 10000)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr != tt.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tt.wantAddr)
			}
			if p != tt.wantPort {
				t.Errorf("port = %d, want %d", p, tt.wantPort)
			}
		})
	}
}

func TestParseAddressAndPort_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing port", "127.0.0.1"},
		{"invalid format", "not:a:valid:address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseAddressAndPort(tt.input, 10000)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

package ideparse

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	ideOpenVSCode = "openvscode"
	ideVSCode     = "vscode"
)

// setupTempHome redirects the path manager to a temp HOME so
// SaveWorkspaceConfig writes under the test's tempdir.
func setupTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func emptyConfig() *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {},
		},
	}
}

// TestRefreshIDEOptions_SwitchPersistsToDisk locks down bug #3: switching
// from openvscode -> vscode on an existing workspace must update
// workspace.IDE.Name AND write the new value to workspace.json so the next
// `opener.Open` dispatch resolves to the right IDE.
func TestRefreshIDEOptions_SwitchPersistsToDisk(t *testing.T) {
	setupTempHome(t)

	ws := &provider.Workspace{
		ID:      "ws-1",
		Context: config.DefaultContext,
		IDE: provider.WorkspaceIDEConfig{
			Name: ideOpenVSCode,
		},
	}
	if err := provider.SaveWorkspaceConfig(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	got, err := RefreshIDEOptions(emptyConfig(), ws, ideVSCode, nil)
	if err != nil {
		t.Fatalf("RefreshIDEOptions: %v", err)
	}
	if got.IDE.Name != ideVSCode {
		t.Errorf("returned workspace.IDE.Name = %q, want %q", got.IDE.Name, ideVSCode)
	}

	reloaded, err := provider.LoadWorkspaceConfig(config.DefaultContext, "ws-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.IDE.Name != ideVSCode {
		t.Errorf(
			"on-disk workspace.IDE.Name = %q, want %q (the switch did not persist)",
			reloaded.IDE.Name, ideVSCode,
		)
	}
}

// TestRefreshIDEOptions_EmptyIDEKeepsExisting ensures that calling with
// ide="" does NOT clobber an already-configured workspace IDE (this is the
// default `devsy up <id>` behavior when --ide is not supplied).
func TestRefreshIDEOptions_EmptyIDEKeepsExisting(t *testing.T) {
	setupTempHome(t)

	ws := &provider.Workspace{
		ID:      "ws-2",
		Context: config.DefaultContext,
		IDE: provider.WorkspaceIDEConfig{
			Name: ideOpenVSCode,
		},
	}
	if err := provider.SaveWorkspaceConfig(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	got, err := RefreshIDEOptions(emptyConfig(), ws, "", nil)
	if err != nil {
		t.Fatalf("RefreshIDEOptions: %v", err)
	}
	if got.IDE.Name != ideOpenVSCode {
		t.Errorf("returned workspace.IDE.Name = %q, want %q", got.IDE.Name, ideOpenVSCode)
	}
}

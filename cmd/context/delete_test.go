package context

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
)

func TestDeleteCmd_RemovesContextDirFromDisk(t *testing.T) {
	pkgconfig.ResetPathManager()
	t.Cleanup(pkgconfig.ResetPathManager)
	home := t.TempDir()
	t.Setenv(pkgconfig.EnvHome, home)
	t.Setenv(pkgconfig.EnvConfig, filepath.Join(home, "config.yaml"))

	// Seed a config.yaml with a second, non-default context so delete is legal.
	cfg := &pkgconfig.Config{
		DefaultContext: pkgconfig.DefaultContext,
		Contexts: map[string]*pkgconfig.ContextConfig{
			pkgconfig.DefaultContext: {},
			"staging":                {},
		},
	}
	if err := pkgconfig.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	contextDir, err := pkgconfig.DefaultPathManager().ContextDir("staging")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(contextDir, "workspaces", "some-workspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}
	if err := cmd.Run(context.Background(), "staging"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(contextDir); !os.IsNotExist(err) {
		t.Fatalf("expected context dir %s to be removed, stat err = %v", contextDir, err)
	}
}

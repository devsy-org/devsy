package context

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

func setupCreateContextTest(t *testing.T) {
	t.Helper()
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.yaml"))
	require.NoError(t, config.SaveConfig(&config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {},
		},
	}))
}

func TestCreateContextRejectsReservedAndUnsafeNames(t *testing.T) {
	for _, name := range []string{
		"con", "prn", "aux", "nul", "com1", "lpt9", "CON", "../escape", "contains spaces", strings.Repeat("a", 49),
	} {
		t.Run(name, func(t *testing.T) {
			setupCreateContextTest(t)
			cmd := &CreateCmd{GlobalFlags: &flags.GlobalFlags{}}
			require.Error(t, cmd.Run(context.Background(), name))
			cfg, err := config.LoadConfig("", "")
			require.NoError(t, err)
			require.NotContains(t, cfg.Contexts, name)
			require.Equal(t, config.DefaultContext, cfg.DefaultContext)
		})
	}
}

func TestCreateContextAcceptsPortableNamesWithinExistingLengthLimit(t *testing.T) {
	setupCreateContextTest(t)
	name := strings.Repeat("a", 48)
	cmd := &CreateCmd{GlobalFlags: &flags.GlobalFlags{}}
	require.NoError(t, cmd.Run(context.Background(), name))
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Contains(t, cfg.Contexts, name)
	require.Equal(t, name, cfg.DefaultContext)
}

func TestLegacyReservedContextRemainsDeletableOnPosix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows device names cannot identify a context directory")
	}
	setupCreateContextTest(t)
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	cfg.Contexts["con"] = &config.ContextConfig{}
	require.NoError(t, config.SaveConfig(cfg))
	cmd := &DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}
	require.NoError(t, cmd.Run(context.Background(), "con"))
	cfg, err = config.LoadConfig("", "")
	require.NoError(t, err)
	require.NotContains(t, cfg.Contexts, "con")
	require.NoError(t, config.CheckPendingContextDeletion())
}

package cmdinternal

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestGHCommandForwardsArgumentsAndConfiguresLogging(t *testing.T) {
	for _, level := range []string{"", "debug", "trace"} {
		t.Run(level, func(t *testing.T) {
			t.Setenv(config.EnvLogLevel, level)
			t.Cleanup(func() { log.Init(log.Config{DefaultLevel: log.DefaultLevel}) })
			parent := NewInternalCmd(&flags.GlobalFlags{})
			parent.PersistentPreRunE = func(*cobra.Command, []string) error {
				return errors.New("parent initialization must not run")
			}
			args := []string{
				"/usr/local/bin/gh",
				"api",
				"--context",
				"repo",
				"--help",
				"--log-level",
				"invalid",
				"--",
				"one two",
			}
			command, _, err := parent.Find(append([]string{"gh"}, args...))
			require.NoError(t, err)
			require.Equal(t, "gh", command.Name())
			var got []string
			command.Run = func(_ *cobra.Command, args []string) { got = args }
			parent.SetArgs(append([]string{"gh"}, args...))
			require.NoError(t, parent.Execute())
			require.Equal(t, args, got)
			want := level
			if want == "" {
				want = log.DefaultLevel
			}
			require.Equal(t, want, log.LevelString())
		})
	}
}

func TestGHWrapperInstallCommandRejectsInvalidPorts(t *testing.T) {
	for _, port := range []string{"0", "65536", "not-a-port"} {
		t.Run(port, func(t *testing.T) {
			cmd := NewInstallGHWrapperCmd()
			cmd.SetArgs([]string{port})
			require.ErrorContains(t, cmd.Execute(), "invalid credentials server port")
		})
	}
}

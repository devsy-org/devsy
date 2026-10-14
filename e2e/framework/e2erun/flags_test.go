package e2erun

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/suite"
)

type E2EFlagsSuite struct{ suite.Suite }

func TestE2EFlagsSuite(t *testing.T) { suite.Run(t, new(E2EFlagsSuite)) }

// e2eRoot hosts the e2e command the way the devsy-ci binary does.
func e2eRoot() *cobra.Command {
	deps := runenvtest.NewRig(runenv.GoosLinux, nil).Dependencies
	root := &cobra.Command{Use: "devsy-ci", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(NewCommand(deps, nil))
	return root
}

func (s *E2EFlagsSuite) TestRunFlagsRequireLabelAndReport() {
	root := e2eRoot()
	root.SetArgs([]string{"e2e", "run"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	err := root.Execute()
	s.Require().Error(err)
	s.Equal(runenv.ExitCodeUsage, runenv.ExitCodeForError(err))
	s.Contains(err.Error(), "label")
}

func (s *E2EFlagsSuite) TestRunFlagDefaults() {
	run, _, err := e2eRoot().Find([]string{"e2e", "run"})
	s.Require().NoError(err)
	for flag, want := range map[string]string{
		"profile": profileLegacy, "podman-mode": podmanModeNone,
		"timeout": "25m0s", "flake-attempts": "1", "label": "", "report": "", "binary": "",
	} {
		f := run.Flags().Lookup(flag)
		s.Require().NotNil(f, flag)
		s.Equal(want, f.DefValue, flag)
	}
}

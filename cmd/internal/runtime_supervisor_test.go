package cmdinternal

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/suite"
)

type RuntimeSupervisorSuite struct{ suite.Suite }

func TestRuntimeSupervisorSuite(t *testing.T) { suite.Run(t, new(RuntimeSupervisorSuite)) }

func (s *RuntimeSupervisorSuite) TestDedicatedPreRun() {
	parent := NewInternalCmd(&flags.GlobalFlags{})
	const configFlag = "--config"
	command, args, err := parent.Find(
		[]string{"runtime-supervisor", "--lease", "3", configFlag, "4"},
	)
	s.Require().NoError(err)
	s.Equal("runtime-supervisor", command.Name())
	s.True(command.Hidden)
	s.True(command.DisableFlagParsing)
	s.Equal([]string{"--lease", "3", configFlag, "4"}, args)
	s.NotNil(command.PersistentPreRunE)
	s.Require().NoError(command.PersistentPreRunE(command, args))
}

package main

import (
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

type CommandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(CommandSuite)) }

func (s *CommandSuite) TestRootCommandSurface() {
	root := newRootCommand(runenvtest.NewRig(runenv.GoosLinux, nil).Dependencies)

	names := []string{}
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	s.ElementsMatch([]string{"docker-wsl"}, names)
}

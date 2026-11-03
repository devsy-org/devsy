package clientimplementation

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type ProviderCommandErrorTestSuite struct {
	suite.Suite
}

func TestProviderCommandErrorTestSuite(t *testing.T) {
	suite.Run(t, new(ProviderCommandErrorTestSuite))
}

func (s *ProviderCommandErrorTestSuite) TestOmitsEmptyAction() {
	cause := errors.New("exit status 255")

	err := providerCommandError("${SSH_PROVIDER} init", nil, cause)

	s.Equal(`run provider command "${SSH_PROVIDER} init": exit status 255`, err.Error())
	s.ErrorIs(err, cause)
}

func (s *ProviderCommandErrorTestSuite) TestIncludesAction() {
	cause := errors.New("exit status 1")
	environ := []string{"UNRELATED=value", provider.CommandEnv + "=ls"}

	err := providerCommandError("${SSH_PROVIDER} command", environ, cause)

	s.Equal(`run provider command "${SSH_PROVIDER} command" (ls): exit status 1`, err.Error())
}

func (s *ProviderCommandErrorTestSuite) TestPlainStartFailureHasNoWindowsHint() {
	cause := &fs.PathError{Op: "fork/exec", Path: "provider", Err: fs.ErrPermission}

	err := providerCommandError("provider", nil, cause)

	s.NotContains(err.Error(), "ERROR_INVALID_PARAMETER")
	s.ErrorIs(err, fs.ErrPermission)
}

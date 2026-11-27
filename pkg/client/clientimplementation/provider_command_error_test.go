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

func (s *ProviderCommandErrorTestSuite) TestKeepsActionAndCause() {
	cause := errors.New("exit status 1")

	err := providerLaunchError(providerAction([]string{provider.CommandEnv + "=ls"}), cause)

	s.Equal("run provider command: exit status 1", err.Error())
	s.ErrorIs(err, cause)
}

func (s *ProviderCommandErrorTestSuite) TestPlainStartFailureHasNoWindowsHint() {
	cause := &fs.PathError{Op: "fork/exec", Path: "provider", Err: fs.ErrPermission}

	err := providerLaunchError("lifecycle", cause)

	s.NotContains(err.Error(), "ERROR_INVALID_PARAMETER")
	s.ErrorIs(err, fs.ErrPermission)
}

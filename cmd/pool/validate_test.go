package pool

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/stretchr/testify/suite"
)

const minimalPool = `apiVersion: devsy.sh/v1alpha1
kind: Pool
metadata:
  name: demo
spec:
  source:
    repo: https://github.com/acme/api
  provider:
    name: docker
  agents:
    claude:
      command: [claude, -p]
`

type ValidateCmdSuite struct {
	suite.Suite
	dir string
}

func TestValidateCmdSuite(t *testing.T) {
	suite.Run(t, new(ValidateCmdSuite))
}

func (s *ValidateCmdSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *ValidateCmdSuite) TestPlainOutput() {
	cmd := s.newCmd(minimalPool, output.ModePlain)
	var out bytes.Buffer
	s.Require().NoError(cmd.Run(&out))
	s.Contains(out.String(), "pool demo is valid")
	s.Contains(out.String(), "policy version: sha256:")
}

func (s *ValidateCmdSuite) TestJSONOutput() {
	cmd := s.newCmd(minimalPool, output.ModeJSON)
	var out bytes.Buffer
	s.Require().NoError(cmd.Run(&out))
	var got validateResult
	s.Require().NoError(json.Unmarshal(out.Bytes(), &got))
	s.Equal("demo", got.Name)
	s.Regexp(`^sha256:[0-9a-f]{64}$`, got.PolicyVersion)
}

func (s *ValidateCmdSuite) TestInvalidFileFails() {
	cmd := s.newCmd(minimalPool+"  unknownField: true\n", output.ModePlain)
	s.Require().ErrorContains(cmd.Run(&bytes.Buffer{}), "unknown field")
}

func (s *ValidateCmdSuite) TestMissingFileFails() {
	cmd := &ValidateCmd{
		GlobalFlags: &flags.GlobalFlags{ResultFormat: output.ModePlain},
		File:        filepath.Join(s.dir, "missing.yaml"),
	}
	s.Require().ErrorContains(cmd.Run(&bytes.Buffer{}), "read pool config")
}

func (s *ValidateCmdSuite) newCmd(content, mode string) *ValidateCmd {
	path := filepath.Join(s.dir, "pool.yaml")
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return &ValidateCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: mode}, File: path}
}

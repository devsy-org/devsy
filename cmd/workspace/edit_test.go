package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/flags/names"
	workspace "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/suite"
)

const (
	testDisplayName = "Node JS"
	testSSHAlias    = "node"
)

type EditCmdSuite struct {
	suite.Suite

	captured workspace.PresentationRequest
	result   *workspace.PresentationResult
	editErr  error
	out      bytes.Buffer
}

func TestEditCmdSuite(t *testing.T) {
	suite.Run(t, new(EditCmdSuite))
}

func (s *EditCmdSuite) SetupTest() {
	s.T().Setenv("DEVSY_HOME", s.T().TempDir())
	s.captured = workspace.PresentationRequest{}
	s.editErr = nil
	s.out.Reset()
	s.result = &workspace.PresentationResult{
		ID:                   testWorkspaceName,
		UID:                  "uid-1",
		Context:              "default",
		DisplayName:          testDisplayName,
		SSHHostAlias:         testSSHAlias,
		EffectiveDisplayName: testDisplayName,
		PreferredSSHHost:     "node.devsy",
		CanonicalSSHHost:     "node-js.devsy",
		SSHEntryUpdated:      true,
	}
}

func (s *EditCmdSuite) TestRequiresAnEditableFlag() {
	err := s.newCmd(formatPlain, testWorkspaceName)
	s.Require().Error(err)
	s.Contains(err.Error(), names.DisplayName)
	s.Contains(err.Error(), names.SSHHostAlias)
	s.Nil(s.captured.DisplayName)
}

func (s *EditCmdSuite) TestExpectedFlagsAloneAreNotEdits() {
	err := s.newCmd(formatPlain, testWorkspaceName, "--"+names.ExpectedUID, "uid-1")
	s.Require().Error(err)
}

func (s *EditCmdSuite) TestOnlyChangedFlagsBecomePointers() {
	s.Require().
		NoError(s.newCmd(formatPlain, testWorkspaceName, "--"+names.DisplayName, testDisplayName))

	s.Equal(testWorkspaceName, s.captured.ID)
	s.Require().NotNil(s.captured.DisplayName)
	s.Equal(testDisplayName, *s.captured.DisplayName)
	s.Nil(s.captured.SSHHostAlias)
	s.Nil(s.captured.ExpectedDisplayName)
	s.Nil(s.captured.ExpectedSSHHostAlias)
	s.Empty(s.captured.ExpectedUID)
}

func (s *EditCmdSuite) TestExplicitEmptyClearsOverride() {
	s.Require().NoError(s.newCmd(
		formatPlain, testWorkspaceName,
		"--"+names.DisplayName, "",
		"--"+names.SSHHostAlias, "",
	))

	s.Require().NotNil(s.captured.DisplayName)
	s.Empty(*s.captured.DisplayName)
	s.Require().NotNil(s.captured.SSHHostAlias)
	s.Empty(*s.captured.SSHHostAlias)
}

func (s *EditCmdSuite) TestExpectedFlagsPassThrough() {
	s.Require().NoError(s.newCmd(
		formatPlain, testWorkspaceName,
		"--"+names.SSHHostAlias, testSSHAlias,
		"--"+names.ExpectedUID, "uid-1",
		"--"+names.ExpectedDisplayName, "",
		"--"+names.ExpectedSSHHostAlias, "old",
	))

	s.Equal("uid-1", s.captured.ExpectedUID)
	s.Require().NotNil(s.captured.ExpectedDisplayName)
	s.Empty(*s.captured.ExpectedDisplayName)
	s.Require().NotNil(s.captured.ExpectedSSHHostAlias)
	s.Equal("old", *s.captured.ExpectedSSHHostAlias)
	s.Nil(s.captured.DisplayName)
}

func (s *EditCmdSuite) TestExpectedFlagsAreHidden() {
	cmd := NewEditCmd(&flags.GlobalFlags{})
	for _, name := range []string{
		names.ExpectedUID,
		names.ExpectedDisplayName,
		names.ExpectedSSHHostAlias,
	} {
		flag := cmd.Flags().Lookup(name)
		s.Require().NotNil(flag, name)
		s.True(flag.Hidden, name)
	}
	s.False(cmd.Flags().Lookup(names.DisplayName).Hidden)
	s.False(cmd.Flags().Lookup(names.SSHHostAlias).Hidden)
}

func (s *EditCmdSuite) TestJSONOutputUsesCamelCaseResult() {
	s.Require().
		NoError(s.newCmd(formatJSON, testWorkspaceName, "--"+names.DisplayName, testDisplayName))

	var got map[string]any
	s.Require().NoError(json.Unmarshal(s.out.Bytes(), &got))
	s.Equal(testWorkspaceName, got["id"])
	s.Equal("uid-1", got["uid"])
	s.Equal(testDisplayName, got["displayName"])
	s.Equal(testSSHAlias, got["sshHostAlias"])
	s.Equal(testDisplayName, got["effectiveDisplayName"])
	s.Equal("node.devsy", got["preferredSSHHost"])
	s.Equal("node-js.devsy", got["canonicalSSHHost"])
	s.Equal(true, got["sshEntryUpdated"])
}

func (s *EditCmdSuite) TestPlainOutputShowsLabelAndHosts() {
	s.Require().
		NoError(s.newCmd(formatPlain, testWorkspaceName, "--"+names.DisplayName, testDisplayName))

	text := s.out.String()
	s.Contains(text, testDisplayName)
	s.Contains(text, "node.devsy")
	s.Contains(text, "node-js.devsy")
}

func (s *EditCmdSuite) TestPlainOutputOmitsCanonicalWhenSame() {
	s.result.PreferredSSHHost = s.result.CanonicalSSHHost
	s.Require().
		NoError(s.newCmd(formatPlain, testWorkspaceName, "--"+names.DisplayName, testDisplayName))

	s.NotContains(s.out.String(), "Canonical SSH Host")
}

func (s *EditCmdSuite) TestPresentationErrorBecomesCLIError() {
	s.editErr = &workspace.PresentationError{
		Field:    "displayName",
		Code:     workspace.PresentationCodeConflict,
		Message:  "label is already used",
		Conflict: "other-ws",
	}

	err := s.newCmd(formatJSON, testWorkspaceName, "--"+names.DisplayName, "Dup")
	s.Require().Error(err)

	cliErr, ok := errors.AsType[*clierr.CLIError](err)
	s.Require().True(ok)
	s.Equal(clierr.Code(workspace.PresentationCodeConflict), cliErr.Code)
	s.Equal("label is already used", cliErr.Message)
	s.Equal(
		map[string]string{"field": "displayName", "conflict": "other-ws"},
		cliErr.Context,
	)
}

func (s *EditCmdSuite) TestPresentationErrorWithoutConflictOmitsKey() {
	s.editErr = &workspace.PresentationError{
		Field:   "sshHostAlias",
		Code:    workspace.PresentationCodeInvalid,
		Message: "bad alias",
	}

	err := s.newCmd(formatPlain, testWorkspaceName, "--"+names.SSHHostAlias, "Bad Alias")
	cliErr, ok := errors.AsType[*clierr.CLIError](err)
	s.Require().True(ok)
	s.Equal(map[string]string{"field": "sshHostAlias"}, cliErr.Context)
}

func (s *EditCmdSuite) TestJoinedRollbackErrorKeepsCodeAndFullMessage() {
	s.editErr = errors.Join(
		&workspace.PresentationError{
			Field:   "workspace",
			Code:    workspace.PresentationCodeStale,
			Message: "workspace changed",
		},
		errors.New("rollback failed"),
	)

	err := s.newCmd(formatPlain, testWorkspaceName, "--"+names.SSHHostAlias, testSSHAlias)
	cliErr, ok := errors.AsType[*clierr.CLIError](err)
	s.Require().True(ok)
	s.Equal(clierr.Code(workspace.PresentationCodeStale), cliErr.Code)
	s.Contains(cliErr.Message, "workspace changed")
	s.Contains(cliErr.Message, "rollback failed")
}

func (s *EditCmdSuite) TestOtherErrorsPassThrough() {
	s.editErr = errors.New("boom")

	err := s.newCmd(formatPlain, testWorkspaceName, "--"+names.DisplayName, "x")
	s.Require().EqualError(err, "boom")
	_, ok := errors.AsType[*clierr.CLIError](err)
	s.False(ok)
}

func (s *EditCmdSuite) newCmd(format string, args ...string) error {
	globalFlags := &flags.GlobalFlags{ResultFormat: format}
	cmd := newEditCmd(
		globalFlags,
		func(req workspace.PresentationRequest) (*workspace.PresentationResult, error) {
			s.captured = req
			return s.result, s.editErr
		},
	)
	cmd.SetOut(&s.out)
	cmd.SetErr(&s.out)
	cmd.SetArgs(args)

	return cmd.Execute()
}

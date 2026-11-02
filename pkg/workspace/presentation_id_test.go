package workspace

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
)

const presentationWorkspaceWeb = "web"

func (s *PresentationTestSuite) location() SSHConfigLocation {
	return SSHConfigLocation{ConfigPath: s.sshConfigPath()}
}

func (s *PresentationTestSuite) ownBlock(host string) string {
	return ssh.MarkerStartPrefix + host + "\nHost " + host + "\n  User vscode\n" +
		ssh.MarkerEndPrefix + host + "\n"
}

func (s *PresentationTestSuite) saveAliasedWeb() {
	s.saveWorkspace(presentationWorkspaceWeb, func(ws *provider.Workspace) {
		ws.SSHHostAlias = presentationAliasBackend
	})
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDRejectsExistingAlias() {
	s.saveAliasedWeb()

	err := ValidateNewWorkspaceID(
		s.devsyConfig, presentationAliasBackend, s.location(),
	)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	s.Require().NoError(ValidateNewWorkspaceID(
		s.devsyConfig, presentationWorkspaceAPI, s.location(),
	))
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDRejectsLiteralHost() {
	s.Require().NoError(os.WriteFile(
		s.sshConfigPath(),
		[]byte("Host taken.devsy\n  HostName example.com\n"),
		0o600,
	))

	err := ValidateNewWorkspaceID(s.devsyConfig, "taken", s.location())
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDIgnoresOwnBlock() {
	s.Require().NoError(os.WriteFile(
		s.sshConfigPath(),
		[]byte(s.ownBlock("api.devsy")),
		0o600,
	))

	s.Require().NoError(ValidateNewWorkspaceID(
		s.devsyConfig, presentationWorkspaceAPI, s.location(),
	))
}

func (s *PresentationTestSuite) TestValidateRenameTargetRejectsAliasAndLiteralHost() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveAliasedWeb()

	err := ValidateRenameTarget(
		s.devsyConfig, presentationWorkspaceAPI, presentationAliasBackend, s.location(),
	)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	s.Require().NoError(os.WriteFile(
		s.sshConfigPath(),
		[]byte("Host taken.devsy\n  HostName example.com\n"),
		0o600,
	))
	err = ValidateRenameTarget(
		s.devsyConfig, presentationWorkspaceAPI, "taken", s.location(),
	)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
}

func (s *PresentationTestSuite) TestValidateRenameTargetIgnoresOwnBlockAndOwnAlias() {
	s.saveWorkspace(presentationWorkspaceAPI, func(ws *provider.Workspace) {
		ws.SSHHostAlias = "mine"
	})
	s.Require().NoError(os.WriteFile(
		s.sshConfigPath(),
		[]byte(s.ownBlock("api.devsy")),
		0o600,
	))

	s.Require().NoError(ValidateRenameTarget(
		s.devsyConfig, presentationWorkspaceAPI, "mine", s.location(),
	))
	s.Require().NoError(ValidateRenameTarget(
		s.devsyConfig, presentationWorkspaceAPI, "api-renamed", s.location(),
	))
}

func (s *PresentationTestSuite) TestRenameIntoAliasFailsBeforeDisruption() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveAliasedWeb()

	err := Rename(context.Background(), RenameOptions{
		DevsyConfig: s.devsyConfig,
		OldName:     presentationWorkspaceAPI,
		NewName:     presentationAliasBackend,
	})
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	stored := s.load(presentationWorkspaceAPI)
	s.Equal(presentationWorkspaceAPI, stored.ID)

	_, loadErr := provider.LoadWorkspaceConfig(presentationContext, presentationAliasBackend)
	s.Require().Error(loadErr)
}

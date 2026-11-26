package workspace

import (
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
)

const (
	reconcileCommittedAlias = presentationAliasBackend
	reconcileCrashedAlias   = "crashed"
	reconcileNextAlias      = "frontend"
)

func (s *PresentationTestSuite) configureCommittedBlock() {
	s.saveWorkspace(presentationWorkspaceAPI, func(ws *provider.Workspace) {
		ws.SSHHostAlias = reconcileCommittedAlias
	})
	s.Require().NoError(ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: s.sshConfigPath(),
		Context:       presentationContext,
		Workspace:     presentationWorkspaceAPI,
		User:          presentationUser,
		Owner:         s.sshOwner(presentationWorkspaceAPI),
		PreferredHost: reconcileCommittedAlias + ".devsy",
	}))
}

func (s *PresentationTestSuite) crashAfterBlockRewrite() {
	_, err := ssh.ReplaceHostAliasesForOwner(
		ssh.AliasReplacement{
			ConfigPath:    s.sshConfigPath(),
			CanonicalHost: presentationWorkspaceAPI + ".devsy",
			PreferredHost: reconcileCrashedAlias + ".devsy",
		},
		s.sshOwner(presentationWorkspaceAPI),
		nil,
	)
	s.Require().NoError(err)

	content := s.readSSHConfig()
	s.Contains(content, "Host "+reconcileCrashedAlias+".devsy api.devsy")
	s.Equal(reconcileCommittedAlias, s.load(presentationWorkspaceAPI).SSHHostAlias)
}

func (s *PresentationTestSuite) readSSHConfig() string {
	data, err := os.ReadFile(s.sshConfigPath())
	s.Require().NoError(err)

	return string(data)
}

func (s *PresentationTestSuite) TestCrashedAliasRewriteConvergesOnNextConfigure() {
	s.configureCommittedBlock()
	s.crashAfterBlockRewrite()

	record := s.load(presentationWorkspaceAPI)
	s.Require().NoError(ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: s.sshConfigPath(),
		Context:       presentationContext,
		Workspace:     presentationWorkspaceAPI,
		User:          presentationUser,
		Owner:         s.sshOwner(presentationWorkspaceAPI),
		PreferredHost: record.PreferredSSHHostCommitted(),
	}))

	content := s.readSSHConfig()
	s.Contains(content, "Host "+reconcileCommittedAlias+".devsy api.devsy")
	s.NotContains(content, reconcileCrashedAlias)
	s.Equal(1, strings.Count(content, ssh.MarkerStartPrefix+"api.devsy"))
}

func (s *PresentationTestSuite) TestCrashedAliasRewriteConvergesOnNextAliasEdit() {
	s.configureCommittedBlock()
	s.crashAfterBlockRewrite()

	req := s.request(presentationWorkspaceAPI)
	req.SSHHostAlias = new(reconcileNextAlias)
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.True(result.SSHEntryUpdated)

	content := s.readSSHConfig()
	s.Contains(content, "Host "+reconcileNextAlias+".devsy api.devsy")
	s.NotContains(content, reconcileCrashedAlias)
	s.Equal(reconcileNextAlias, s.load(presentationWorkspaceAPI).SSHHostAlias)
}

func (s *PresentationTestSuite) TestAliasEditNeverRewritesForeignOwnerBlock() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	foreign, err := ssh.NewOwner(presentationContext, "uid-recreated")
	s.Require().NoError(err)
	s.Require().NoError(ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: s.sshConfigPath(),
		Context:       presentationContext,
		Workspace:     presentationWorkspaceAPI,
		User:          presentationUser,
		Owner:         foreign,
	}))
	before := s.readSSHConfig()

	req := s.request(presentationWorkspaceAPI)
	req.SSHHostAlias = new(reconcileNextAlias)
	_, err = EditPresentation(req)

	s.Require().Error(err)
	s.Equal(before, s.readSSHConfig())
	s.Empty(s.load(presentationWorkspaceAPI).SSHHostAlias)
}

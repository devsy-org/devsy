package workspace

import (
	"context"
	"os"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
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

func (s *PresentationTestSuite) saveOtherContextWorkspace(id, alias string) {
	s.Require().NoError(provider.SaveWorkspaceConfig(&provider.Workspace{
		ID:           id,
		UID:          "uid-" + id,
		Context:      presentationWorkspaceOther,
		SSHHostAlias: alias,
	}))
}

func (s *PresentationTestSuite) TestAliasInOtherContextBlocksEdit() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveOtherContextWorkspace(presentationWorkspaceWeb, presentationAliasBackend)

	req := s.request(presentationWorkspaceAPI)
	req.SSHHostAlias = new(presentationAliasBackend)
	_, err := EditPresentation(req)
	conflict := s.requirePresentationErr(
		err,
		PresentationFieldSSHHostAlias,
		PresentationCodeConflict,
	)
	s.Equal(presentationWorkspaceWeb, conflict.Conflict)
	s.Contains(conflict.Message, `"web"`)
	s.Contains(conflict.Message, `"other"`)
	s.Empty(s.load(presentationWorkspaceAPI).SSHHostAlias)
}

func (s *PresentationTestSuite) TestCanonicalHostInOtherContextBlocksAlias() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveOtherContextWorkspace(presentationWorkspaceWeb, "")

	req := s.request(presentationWorkspaceAPI)
	req.SSHHostAlias = new(presentationWorkspaceWeb)
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
}

func (s *PresentationTestSuite) TestAliasInOtherContextBlocksValidation() {
	s.saveOtherContextWorkspace(presentationWorkspaceWeb, presentationAliasBackend)

	s.requireErr(
		s.validate("", presentationAliasBackend),
		PresentationFieldSSHHostAlias,
		PresentationCodeConflict,
	)
	s.requireErr(
		ValidateNewWorkspaceID(s.devsyConfig, presentationAliasBackend, s.location()),
		PresentationFieldSSHHostAlias,
		PresentationCodeConflict,
	)
	s.requireErr(
		ValidateRenameTarget(
			s.devsyConfig, presentationWorkspaceAPI, presentationAliasBackend, s.location(),
		),
		PresentationFieldSSHHostAlias,
		PresentationCodeConflict,
	)
}

func (s *PresentationTestSuite) TestSameIDInOwnContextIsNotOther() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)

	s.Require().NoError(ValidateRenameTarget(
		s.devsyConfig, presentationWorkspaceAPI, "api-renamed", s.location(),
	))
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDWaitsForPresentationLock() {
	unlock, err := lockPresentation()
	s.Require().NoError(err)

	done := make(chan error, 1)
	go func() {
		done <- ValidateNewWorkspaceID(s.devsyConfig, presentationAliasBackend, s.location())
	}()

	select {
	case err := <-done:
		unlock()
		s.FailNow("validation finished while the presentation lock was held", "err: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("validation did not resume after the presentation lock was released")
	}
	s.requireLockAvailable()
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDSeesRecordSavedWhileWaiting() {
	unlock, err := lockPresentation()
	s.Require().NoError(err)

	done := make(chan error, 1)
	go func() {
		done <- ValidateNewWorkspaceID(s.devsyConfig, presentationAliasBackend, s.location())
	}()

	s.saveAliasedWeb()
	unlock()

	select {
	case err := <-done:
		s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	case <-time.After(5 * time.Second):
		s.FailNow("validation did not resume after the presentation lock was released")
	}
}

func (s *PresentationTestSuite) TestValidateNewWorkspaceIDReleasesPresentationLockOnError() {
	s.saveAliasedWeb()

	err := ValidateNewWorkspaceID(s.devsyConfig, presentationAliasBackend, s.location())
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	s.requireLockAvailable()
}

func (s *PresentationTestSuite) TestValidateRenameTargetDoesNotAcquirePresentationLock() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)

	unlock, err := lockPresentation()
	s.Require().NoError(err)
	defer unlock()

	done := make(chan error, 1)
	go func() {
		done <- ValidateRenameTarget(
			s.devsyConfig, presentationWorkspaceAPI, "api-renamed", s.location(),
		)
	}()

	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("rename target validation blocked on the presentation lock")
	}
}

func (s *PresentationTestSuite) requireLockAvailable() {
	acquired := make(chan error, 1)
	go func() {
		unlock, err := lockPresentation()
		if err == nil {
			unlock()
		}
		acquired <- err
	}()

	select {
	case err := <-acquired:
		s.Require().NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("presentation lock was not released")
	}
}

func (s *PresentationTestSuite) TestRenameWaitsForPresentationLock() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)

	unlock, err := lockPresentation()
	s.Require().NoError(err)

	done := make(chan error, 1)
	go func() {
		done <- Rename(context.Background(), RenameOptions{
			DevsyConfig: s.devsyConfig,
			OldName:     presentationWorkspaceAPI,
			NewName:     presentationAliasBackend,
		})
	}()
	s.saveAliasedWeb()

	select {
	case err := <-done:
		unlock()
		s.FailNow("rename finished while the presentation lock was held", "err: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-done:
		s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	case <-time.After(5 * time.Second):
		s.FailNow("rename did not resume after the presentation lock was released")
	}
	s.requireLockAvailable()
}

func (s *PresentationTestSuite) TestRenameReleasesPresentationLockOnError() {
	err := Rename(context.Background(), RenameOptions{
		DevsyConfig: s.devsyConfig,
		OldName:     "missing",
		NewName:     "other-name",
	})
	s.Require().Error(err)
	s.requireLockAvailable()

	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveAliasedWeb()
	err = Rename(context.Background(), RenameOptions{
		DevsyConfig: s.devsyConfig,
		OldName:     presentationWorkspaceAPI,
		NewName:     presentationAliasBackend,
	})
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	s.requireLockAvailable()
}

func (s *PresentationTestSuite) TestEditPresentationWaitsForLockHeldByRename() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)

	unlock, err := lockPresentation()
	s.Require().NoError(err)

	done := make(chan error, 1)
	go func() {
		req := s.request(presentationWorkspaceAPI)
		req.DisplayName = new("Waiter")
		_, err := EditPresentation(req)
		done <- err
	}()

	select {
	case err := <-done:
		unlock()
		s.FailNow("edit finished while the presentation lock was held", "err: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-done:
		s.Require().NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("edit did not resume after the presentation lock was released")
	}
	s.Equal("Waiter", s.load(presentationWorkspaceAPI).DisplayName)
}

func (s *PresentationTestSuite) TestValidatePreferredSSHHostRejectsOtherContextAlias() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	s.saveOtherContextWorkspace(presentationWorkspaceWeb, presentationAliasBackend)

	err := ValidatePreferredSSHHost(
		presentationContext, presentationWorkspaceAPI, presentationAliasBackend+".devsy",
	)
	conflict := s.requirePresentationErr(
		err,
		PresentationFieldSSHHostAlias,
		PresentationCodeConflict,
	)
	s.Equal(presentationWorkspaceWeb, conflict.Conflict)
}

func (s *PresentationTestSuite) TestValidatePreferredSSHHostAllowsOwnAndCanonicalHosts() {
	s.saveWorkspace(presentationWorkspaceAPI, func(ws *provider.Workspace) {
		ws.SSHHostAlias = presentationAliasBackend
	})
	s.saveOtherContextWorkspace("docs", "")

	s.Require().NoError(ValidatePreferredSSHHost(
		presentationContext, presentationWorkspaceAPI, presentationAliasBackend+".devsy",
	))
	s.Require().NoError(ValidatePreferredSSHHost(
		presentationContext, presentationWorkspaceAPI, presentationWorkspaceAPI+".devsy",
	))
	s.Require().NoError(ValidatePreferredSSHHost(presentationContext, presentationWorkspaceAPI, ""))
}

func (s *PresentationTestSuite) TestCheckExistsReportsMissingAndCanceled() {
	s.saveWorkspace(presentationWorkspaceAPI, nil)
	cfg := &config.Config{
		DefaultContext: presentationContext,
		Contexts:       map[string]*config.ContextConfig{presentationContext: {}},
	}
	opts := LookupOptions{ID: presentationWorkspaceAPI}

	found, err := CheckExists(context.Background(), cfg, opts)
	s.Require().NoError(err)
	s.True(found)

	found, err = CheckExists(
		context.Background(), cfg, LookupOptions{ID: presentationWorkspaceWeb},
	)
	s.Require().NoError(err)
	s.False(found)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	found, err = CheckExists(ctx, cfg, LookupOptions{ID: presentationWorkspaceWeb})
	s.Require().ErrorIs(err, context.Canceled)
	s.False(found)
}

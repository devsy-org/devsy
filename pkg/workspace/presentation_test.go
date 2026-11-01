package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/suite"
)

const presentationContext = "default"

type PresentationTestSuite struct {
	suite.Suite

	devsyConfig *config.Config
	sshDir      string
}

func TestPresentationTestSuite(t *testing.T) {
	suite.Run(t, new(PresentationTestSuite))
}

func (s *PresentationTestSuite) SetupTest() {
	setupTestPathManager(s.T())
	s.devsyConfig = &config.Config{DefaultContext: presentationContext}
	s.sshDir = s.T().TempDir()
}

func (s *PresentationTestSuite) sshConfigPath() string {
	return filepath.Join(s.sshDir, "config")
}

func (s *PresentationTestSuite) saveWorkspace(
	id string,
	mutate func(ws *provider.Workspace),
) *provider.Workspace {
	ws := &provider.Workspace{
		ID:            id,
		UID:           "uid-" + id,
		Context:       presentationContext,
		SSHConfigPath: s.sshConfigPath(),
	}
	if mutate != nil {
		mutate(ws)
	}
	s.Require().NoError(provider.SaveWorkspaceConfig(ws))

	return ws
}

func (s *PresentationTestSuite) load(id string) *provider.Workspace {
	ws, err := provider.LoadWorkspaceConfig(presentationContext, id)
	s.Require().NoError(err)

	return ws
}

func (s *PresentationTestSuite) request(id string) PresentationRequest {
	return PresentationRequest{DevsyConfig: s.devsyConfig, ID: id}
}

func (s *PresentationTestSuite) requireErr(
	err error,
	field, code string,
) *PresentationError {
	s.Require().Error(err)
	var presentationErr *PresentationError
	s.Require().True(errors.As(err, &presentationErr), "error is %T: %v", err, err)
	s.Equal(field, presentationErr.Field)
	s.Equal(code, presentationErr.Code)

	return presentationErr
}

func (s *PresentationTestSuite) TestNormalizeDisplayName() {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "blank clears", input: "   ", want: ""},
		{name: "trimmed", input: "  My Project  ", want: "My Project"},
		{
			name:  "preserves interior spaces and casing",
			input: "My  Big PROJECT",
			want:  "My  Big PROJECT",
		},
		{name: "nfc composes", input: "Cafe\u0301", want: "Caf\u00e9"},
		{name: "tab rejected", input: "a\tb", wantErr: true},
		{name: "newline rejected", input: "a\nb", wantErr: true},
		{name: "nul rejected", input: "a\x00b", wantErr: true},
		{name: "max length", input: strings.Repeat("a", 120), want: strings.Repeat("a", 120)},
		{name: "over max length", input: strings.Repeat("a", 121), wantErr: true},
		{
			name:  "counts code points",
			input: strings.Repeat("\u00e9", 120),
			want:  strings.Repeat("\u00e9", 120),
		},
		{name: "over max code points", input: strings.Repeat("\u00e9", 121), wantErr: true},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := NormalizeDisplayName(tt.input)
			if tt.wantErr {
				s.requireErr(err, PresentationFieldDisplayName, PresentationCodeInvalid)
				return
			}
			s.Require().NoError(err)
			s.Equal(tt.want, got)
		})
	}
}

func (s *PresentationTestSuite) TestNormalizeSSHHostAlias() {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "single char", input: "a", want: "a"},
		{name: "digits", input: "9", want: "9"},
		{name: "hyphen inside", input: "a-b", want: "a-b"},
		{name: "uppercase lowered", input: "MyBox", want: "mybox"},
		{name: "max length", input: strings.Repeat("a", 48), want: strings.Repeat("a", 48)},
		{name: "over max length", input: strings.Repeat("a", 49), wantErr: true},
		{name: "leading hyphen", input: "-a", wantErr: true},
		{name: "trailing hyphen", input: "a-", wantErr: true},
		{name: "single hyphen", input: "-", wantErr: true},
		{name: "suffix", input: "box.devsy", wantErr: true},
		{name: "dot", input: "a.b", wantErr: true},
		{name: "space", input: "a b", wantErr: true},
		{name: "wildcard star", input: "a*", wantErr: true},
		{name: "wildcard question", input: "a?", wantErr: true},
		{name: "slash", input: "a/b", wantErr: true},
		{name: "underscore", input: "a_b", wantErr: true},
		{name: "non ascii", input: "caf\u00e9", wantErr: true},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := NormalizeSSHHostAlias(tt.input)
			if tt.wantErr {
				s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeInvalid)
				return
			}
			s.Require().NoError(err)
			s.Equal(tt.want, got)
		})
	}
}

func (s *PresentationTestSuite) TestEditSetsLabelAndAlias() {
	s.saveWorkspace("api", nil)

	req := s.request("api")
	req.DisplayName = new("My API")
	req.SSHHostAlias = new("backend")
	result, err := EditPresentation(req)
	s.Require().NoError(err)

	s.Equal("api", result.ID)
	s.Equal("uid-api", result.UID)
	s.Equal(presentationContext, result.Context)
	s.Equal("My API", result.DisplayName)
	s.Equal("backend", result.SSHHostAlias)
	s.Equal("My API", result.EffectiveDisplayName)
	s.Equal("backend.devsy", result.PreferredSSHHost)
	s.Equal("api.devsy", result.CanonicalSSHHost)
	s.False(result.SSHEntryUpdated)

	stored := s.load("api")
	s.Equal("My API", stored.DisplayName)
	s.Equal("backend", stored.SSHHostAlias)
	s.Equal("uid-api", stored.UID)
}

func (s *PresentationTestSuite) TestOmittedVersusExplicitEmpty() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.DisplayName = "Label"
		ws.SSHHostAlias = "backend"
	})

	req := s.request("api")
	req.DisplayName = new("Renamed")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Renamed", result.DisplayName)
	s.Equal("backend", result.SSHHostAlias)

	req = s.request("api")
	req.SSHHostAlias = new("")
	result, err = EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Renamed", result.DisplayName)
	s.Empty(result.SSHHostAlias)
	s.Equal("api.devsy", result.PreferredSSHHost)

	req = s.request("api")
	req.DisplayName = new("")
	result, err = EditPresentation(req)
	s.Require().NoError(err)
	s.Empty(result.DisplayName)
	s.Equal("api", result.EffectiveDisplayName)

	stored := s.load("api")
	s.Empty(stored.DisplayName)
	s.Empty(stored.SSHHostAlias)
}

func (s *PresentationTestSuite) TestEmptyRequestChangesNothing() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.DisplayName = "Label"
		ws.SSHHostAlias = "backend"
	})

	result, err := EditPresentation(s.request("api"))
	s.Require().NoError(err)
	s.Equal("Label", result.DisplayName)
	s.Equal("backend", result.SSHHostAlias)
	s.False(result.SSHEntryUpdated)
}

func (s *PresentationTestSuite) TestAliasEqualToOwnIDNormalizesToNoOverride() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.SSHHostAlias = "backend"
	})

	req := s.request("api")
	req.SSHHostAlias = new("api")
	result, err := EditPresentation(req)
	s.Require().NoError(err)

	s.Empty(result.SSHHostAlias)
	s.Equal("api.devsy", result.PreferredSSHHost)
	s.Empty(s.load("api").SSHHostAlias)
}

func (s *PresentationTestSuite) TestInvalidInputRejected() {
	s.saveWorkspace("api", nil)

	req := s.request("api")
	req.SSHHostAlias = new("bad.devsy")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeInvalid)

	req = s.request("api")
	req.DisplayName = new("bad\x07label")
	_, err = EditPresentation(req)
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeInvalid)

	stored := s.load("api")
	s.Empty(stored.DisplayName)
	s.Empty(stored.SSHHostAlias)
}

func (s *PresentationTestSuite) TestDuplicateLabelConflicts() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", func(ws *provider.Workspace) {
		ws.DisplayName = "Frontend App"
	})

	req := s.request("api")
	req.DisplayName = new("FRONTEND APP")
	_, err := EditPresentation(req)
	conflict := s.requireErr(err, PresentationFieldDisplayName, PresentationCodeConflict)
	s.Equal("web", conflict.Conflict)
	s.Contains(conflict.Message, "web")

	s.Empty(s.load("api").DisplayName)
}

func (s *PresentationTestSuite) TestLabelConflictsWithOtherWorkspaceID() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", nil)

	req := s.request("api")
	req.DisplayName = new("WEB")
	_, err := EditPresentation(req)
	conflict := s.requireErr(err, PresentationFieldDisplayName, PresentationCodeConflict)
	s.Equal("web", conflict.Conflict)
}

func (s *PresentationTestSuite) TestLabelEqualToOwnIDAllowed() {
	s.saveWorkspace("api", nil)

	req := s.request("api")
	req.DisplayName = new("Api")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Api", result.DisplayName)
}

func (s *PresentationTestSuite) TestLabelConflictFoldsUnicode() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", func(ws *provider.Workspace) {
		ws.DisplayName = "Caf\u00e9"
	})

	req := s.request("api")
	req.DisplayName = new("CAFE\u0301")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeConflict)
}

func (s *PresentationTestSuite) TestLabelConflictIsScopedToContext() {
	s.saveWorkspace("api", nil)
	other := &provider.Workspace{
		ID:          "web",
		UID:         "uid-web",
		Context:     "other",
		DisplayName: "Shared",
	}
	s.Require().NoError(provider.SaveWorkspaceConfig(other))

	req := s.request("api")
	req.DisplayName = new("Shared")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Shared", result.DisplayName)
}

func (s *PresentationTestSuite) TestAliasConflictsWithOtherCanonicalHost() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", nil)

	req := s.request("api")
	req.SSHHostAlias = new("web")
	_, err := EditPresentation(req)
	conflict := s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	s.Equal("web", conflict.Conflict)
	s.Empty(s.load("api").SSHHostAlias)
}

func (s *PresentationTestSuite) TestAliasConflictsWithOtherPreferredHost() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", func(ws *provider.Workspace) {
		ws.SSHHostAlias = "front"
	})

	req := s.request("api")
	req.SSHHostAlias = new("FRONT")
	_, err := EditPresentation(req)
	conflict := s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	s.Equal("web", conflict.Conflict)
}

func (s *PresentationTestSuite) TestAliasConflictIndependentOfLabelConflict() {
	s.saveWorkspace("api", nil)
	s.saveWorkspace("web", func(ws *provider.Workspace) {
		ws.DisplayName = "Frontend"
	})

	req := s.request("api")
	req.DisplayName = new("Frontend")
	req.SSHHostAlias = new("fine")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeConflict)

	req = s.request("api")
	req.DisplayName = new("Backend")
	req.SSHHostAlias = new("web")
	_, err = EditPresentation(req)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	req = s.request("api")
	req.DisplayName = new("Backend")
	req.SSHHostAlias = new("fine")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Backend", result.DisplayName)
	s.Equal("fine", result.SSHHostAlias)
}

func (s *PresentationTestSuite) TestAliasConflictsWithSSHConfigHost() {
	s.saveWorkspace("api", nil)
	s.Require().NoError(os.WriteFile(
		s.sshConfigPath(),
		[]byte("Host taken.devsy\n  HostName example.com\n"),
		0o600,
	))

	req := s.request("api")
	req.SSHHostAlias = new("taken")
	_, err := EditPresentation(req)
	conflict := s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)
	s.Contains(conflict.Conflict, "taken.devsy")
	s.Empty(s.load("api").SSHHostAlias)
}

func (s *PresentationTestSuite) TestAliasWithoutManagedBlockOnlyPersists() {
	s.saveWorkspace("api", nil)
	original := "Host other\n  HostName example.com\n"
	s.Require().NoError(os.WriteFile(s.sshConfigPath(), []byte(original), 0o600))

	req := s.request("api")
	req.SSHHostAlias = new("backend")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.False(result.SSHEntryUpdated)
	s.Equal("backend", s.load("api").SSHHostAlias)

	content, err := os.ReadFile(s.sshConfigPath())
	s.Require().NoError(err)
	s.Equal(original, string(content))
}

func (s *PresentationTestSuite) TestPlatformWorkspaceRejected() {
	s.saveWorkspace("remote", func(ws *provider.Workspace) {
		ws.Platform = &provider.PlatformMetadata{}
	})

	req := s.request("remote")
	req.DisplayName = new("Label")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldWorkspace, PresentationCodeManaged)

	s.Empty(s.load("remote").DisplayName)
}

func (s *PresentationTestSuite) TestMissingWorkspaceNotFound() {
	req := s.request("missing")
	req.DisplayName = new("Label")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldWorkspace, PresentationCodeNotFound)
}

func (s *PresentationTestSuite) TestMissingIDInvalid() {
	_, err := EditPresentation(PresentationRequest{DevsyConfig: s.devsyConfig})
	s.requireErr(err, PresentationFieldWorkspace, PresentationCodeInvalid)
}

func (s *PresentationTestSuite) TestStaleUID() {
	s.saveWorkspace("api", nil)

	req := s.request("api")
	req.ExpectedUID = "uid-other"
	req.DisplayName = new("Label")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldWorkspace, PresentationCodeStale)
	s.Empty(s.load("api").DisplayName)

	req.ExpectedUID = "uid-api"
	_, err = EditPresentation(req)
	s.Require().NoError(err)
}

func (s *PresentationTestSuite) TestStaleDisplayNameExpectation() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.DisplayName = "Current"
	})

	req := s.request("api")
	req.ExpectedDisplayName = new("Outdated")
	req.DisplayName = new("Next")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeStale)
	s.Equal("Current", s.load("api").DisplayName)

	req.ExpectedDisplayName = new("  Current ")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Next", result.DisplayName)
}

func (s *PresentationTestSuite) TestDisplayNameExpectationForUnsetOverride() {
	s.saveWorkspace("api", nil)

	for _, expected := range []string{"", "api"} {
		req := s.request("api")
		req.ExpectedDisplayName = new(expected)
		req.DisplayName = new("Label")
		_, err := EditPresentation(req)
		s.Require().NoError(err, "expected %q", expected)

		reset := s.request("api")
		reset.DisplayName = new("")
		_, err = EditPresentation(reset)
		s.Require().NoError(err)
	}

	req := s.request("api")
	req.ExpectedDisplayName = new("Something")
	req.DisplayName = new("Label")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeStale)
}

func (s *PresentationTestSuite) TestStaleAliasExpectation() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.SSHHostAlias = "backend"
	})

	req := s.request("api")
	req.ExpectedSSHHostAlias = new("other")
	req.SSHHostAlias = new("next")
	_, err := EditPresentation(req)
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeStale)
	s.Equal("backend", s.load("api").SSHHostAlias)

	req.ExpectedSSHHostAlias = new("BACKEND")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("next", result.SSHHostAlias)
}

func (s *PresentationTestSuite) TestAliasExpectationForUnsetOverride() {
	s.saveWorkspace("api", nil)

	for _, expected := range []string{"", "api"} {
		req := s.request("api")
		req.ExpectedSSHHostAlias = new(expected)
		req.SSHHostAlias = new("backend")
		_, err := EditPresentation(req)
		s.Require().NoError(err, "expected %q", expected)

		reset := s.request("api")
		reset.SSHHostAlias = new("")
		_, err = EditPresentation(reset)
		s.Require().NoError(err)
	}
}

func (s *PresentationTestSuite) TestLabelOnlyEditIgnoresUnreadableSSHConfig() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.SSHHostAlias = "backend"
		ws.SSHConfigPath = s.sshDir
		ws.SSHConfigIncludePath = filepath.Join(s.sshDir, "missing", "devsy_config")
	})

	req := s.request("api")
	req.DisplayName = new("Label")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Label", result.DisplayName)
	s.Equal("backend", result.SSHHostAlias)
	s.False(result.SSHEntryUpdated)

	req = s.request("api")
	req.DisplayName = new("Another")
	req.SSHHostAlias = new("backend")
	result, err = EditPresentation(req)
	s.Require().NoError(err)
	s.Equal("Another", result.DisplayName)

	_, statErr := os.Stat(filepath.Join(s.sshDir, "missing"))
	s.True(os.IsNotExist(statErr))
}

func (s *PresentationTestSuite) TestAliasEditFailsWithUnreadableSSHConfig() {
	s.saveWorkspace("api", func(ws *provider.Workspace) {
		ws.SSHConfigPath = s.sshDir
	})

	req := s.request("api")
	req.SSHHostAlias = new("backend")
	_, err := EditPresentation(req)
	s.Require().Error(err)
	s.Empty(s.load("api").SSHHostAlias)
}

func (s *PresentationTestSuite) TestValidatePresentation() {
	s.saveWorkspace("web", func(ws *provider.Workspace) {
		ws.DisplayName = "Frontend"
		ws.SSHHostAlias = "front"
	})

	s.Require().NoError(ValidatePresentation(s.devsyConfig, "api", "", ""))
	s.Require().NoError(ValidatePresentation(s.devsyConfig, "api", "Backend", "back"))
	s.Require().NoError(ValidatePresentation(s.devsyConfig, "api", "", "api"))

	err := ValidatePresentation(s.devsyConfig, "api", "frontend", "")
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeConflict)

	err = ValidatePresentation(s.devsyConfig, "api", "", "front")
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	err = ValidatePresentation(s.devsyConfig, "api", "", "web")
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeConflict)

	err = ValidatePresentation(s.devsyConfig, "api", "", "bad.devsy")
	s.requireErr(err, PresentationFieldSSHHostAlias, PresentationCodeInvalid)

	err = ValidatePresentation(s.devsyConfig, "api", "bad\x00", "")
	s.requireErr(err, PresentationFieldDisplayName, PresentationCodeInvalid)
}

func (s *PresentationTestSuite) TestPresentationErrorMessage() {
	err := newConflictError(PresentationFieldDisplayName, "web", "already used")
	s.Equal("displayName: already used", err.Error())
}

func (s *PresentationTestSuite) TestAliasEditRewritesManagedHostLine() {
	s.saveWorkspace("api", nil)
	s.Require().NoError(ssh.ConfigureSSHConfig(ssh.SSHConfigParams{
		SSHConfigPath: s.sshConfigPath(),
		Context:       presentationContext,
		Workspace:     "api",
		User:          "vscode",
		UID:           "uid-api",
	}))

	req := s.request("api")
	req.SSHHostAlias = new("backend")
	result, err := EditPresentation(req)
	s.Require().NoError(err)
	s.True(result.SSHEntryUpdated)

	content, err := os.ReadFile(s.sshConfigPath())
	s.Require().NoError(err)
	s.Contains(string(content), "Host backend.devsy api.devsy")

	req = s.request("api")
	req.SSHHostAlias = new("")
	result, err = EditPresentation(req)
	s.Require().NoError(err)
	s.True(result.SSHEntryUpdated)

	content, err = os.ReadFile(s.sshConfigPath())
	s.Require().NoError(err)
	s.NotContains(string(content), "backend.devsy")
	s.Contains(string(content), "Host api.devsy")
}

package up

import (
	"context"
	"fmt"
	"testing"

	secretspkg "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
)

const (
	workspaceEnvLogLevel          = "LOG_LEVEL"
	workspaceEnvLiteralAssignment = "LOG_LEVEL=custom-value"
)

type WorkspaceEnvTestSuite struct {
	suite.Suite
}

func TestWorkspaceEnvTestSuite(t *testing.T) {
	suite.Run(t, new(WorkspaceEnvTestSuite))
}

func (s *WorkspaceEnvTestSuite) TestAttachmentFillsMissingTarget() {
	base, err := indexWorkspaceEnv([]string{"LOCAL=value"})
	s.Require().NoError(err)
	requests, err := filterWorkspaceEnvRequests(base, []envVarRequest{{
		ref: localRef("ATTACHED"), target: "ATTACHED", origin: envVarAttached,
	}})
	s.Require().NoError(err)
	s.Require().Len(requests, 1)
	got, err := composeWorkspaceEnv(base, []resolvedEnvVar{{
		assignment: "ATTACHED=managed",
	}})
	s.Require().NoError(err)
	s.Assert().Equal([]string{"ATTACHED=managed", "LOCAL=value"}, got)
}

func (s *WorkspaceEnvTestSuite) TestLiteralWorkspaceEnvShadowsAttachment() {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLiteralAssignment}
	resolver := secretspkg.NewResolver()
	s.Require().NoError(resolver.Register("local", "local", fixedSource{
		values: map[string]string{workspaceEnvLogLevel: "stored-value"},
	}))

	err := cmd.applyEnvVars(s.T().Context(), testEnvConfig(workspaceEnvLogLevel), resolver)
	s.Require().NoError(err)
	s.Assert().Equal([]string{workspaceEnvLiteralAssignment}, cmd.WorkspaceEnv)
}

func (s *WorkspaceEnvTestSuite) TestShadowingDoesNotDependOnValueLexicalOrder() {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLogLevel + "=stored-value"}
	resolver := secretspkg.NewResolver()
	s.Require().NoError(resolver.Register("local", "local", fixedSource{
		values: map[string]string{workspaceEnvLogLevel: "custom-value"},
	}))

	err := cmd.applyEnvVars(
		s.T().Context(),
		testEnvConfig(workspaceEnvLogLevel),
		resolver,
	)
	s.Require().NoError(err)
	s.Assert().Equal([]string{workspaceEnvLogLevel + "=stored-value"}, cmd.WorkspaceEnv)
}

func (s *WorkspaceEnvTestSuite) TestShadowedAttachmentIsNotResolved() {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{workspaceEnvLogLevel + "=local"}
	resolver := secretspkg.NewResolver()
	s.Require().NoError(resolver.Register("local", "local", unavailableEnvSource{}))

	err := cmd.applyEnvVars(s.T().Context(), testEnvConfig(workspaceEnvLogLevel), resolver)
	s.Require().NoError(err)
	s.Assert().Equal([]string{workspaceEnvLogLevel + "=local"}, cmd.WorkspaceEnv)
}

func (s *WorkspaceEnvTestSuite) TestExplicitManagedTargetConflictsWithLiteralTarget() {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{"APP_MODE=local"}
	cmd.EnvVars = []string{"STORED_MODE=APP_MODE"}
	err := cmd.applyEnvVars(s.T().Context(), testEnvConfig(), secretspkg.NewResolver())
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "APP_MODE")
	s.Assert().Contains(err.Error(), "both --workspace-env and --env")
	s.Assert().NotContains(err.Error(), "local")
	s.Assert().Equal([]string{"APP_MODE=local"}, cmd.WorkspaceEnv)
}

func (s *WorkspaceEnvTestSuite) TestDuplicateLiteralTargetRejected() {
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{"APP_MODE=a", "APP_MODE=b"}
	err := cmd.applyEnvVars(s.T().Context(), testEnvConfig(), secretspkg.NewResolver())
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), `"APP_MODE"`)
	s.Assert().NotContains(err.Error(), "=a")
	s.Assert().NotContains(err.Error(), "=b")
}

func (s *WorkspaceEnvTestSuite) TestValueMayContainEquals() {
	assignment, err := parseWorkspaceEnvAssignment("TOKEN=one=two")
	s.Require().NoError(err)
	s.Assert().Equal(workspaceEnvAssignment{name: "TOKEN", value: "one=two"}, assignment)
}

func (s *WorkspaceEnvTestSuite) TestOutputHasUniqueTargets() {
	base, err := indexWorkspaceEnv([]string{"A=one"})
	s.Require().NoError(err)
	got, err := composeWorkspaceEnv(base, []resolvedEnvVar{{
		assignment: "B=two",
	}})
	s.Require().NoError(err)
	s.Assert().Equal([]string{"A=one", "B=two"}, got)
	indexed, err := indexWorkspaceEnv(got)
	s.Require().NoError(err)
	s.Assert().Len(indexed, 2)
}

type unavailableEnvSource struct{}

func (unavailableEnvSource) Get(_ context.Context, name string) (secretspkg.ResolvedSecret, error) {
	return secretspkg.ResolvedSecret{}, fmt.Errorf("unexpected resolution of %s", name)
}

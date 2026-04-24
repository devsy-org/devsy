package setup

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type LifecycleHookTestSuite struct {
	suite.Suite
}

func (s *LifecycleHookTestSuite) TestStringCommandWithQuotes() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{`echo "hello world"`}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"sh", "-c", `echo "hello world"`}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommand() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"echo", "hello", "world"}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"echo", "hello", "world"}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommandWithShellWrapper() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"sh", "-c", `echo "test"`}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"sh", "-c", `echo "test"`}, args)
}

func (s *LifecycleHookTestSuite) TestStringCommandWithUserSwitch() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{`echo "hello"`}
	args := buildCommandArgs(c, "otheruser", currentUser.Username)
	assert.Equal(s.T(), []string{"su", "otheruser", "-c", `echo "hello"`}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommandWithUserSwitch() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"echo", "hello"}
	args := buildCommandArgs(c, "otheruser", currentUser.Username)
	assert.Equal(s.T(), []string{"su", "otheruser", "-c", "echo hello"}, args)
}

func (s *LifecycleHookTestSuite) TestSymlinkWithQuotes() {
	if os.Getuid() != 0 {
		s.T().Skip("Requires root")
	}

	testLink := "/tmp/devsy_test_link"
	_ = os.Remove(testLink)
	defer func() { _ = os.Remove(testLink) }()

	cmd := exec.Command("sh", "-c", `ln -sf "$(command -v ls)" `+testLink)
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "Output: %s", output)

	target, err := os.Readlink(testLink)
	s.Require().NoError(err)
	s.Require().NotEmpty(target, "symlink target should not be empty")
	assert.NotEqual(s.T(), byte('"'), target[0])
	assert.NotEqual(s.T(), byte('"'), target[len(target)-1])
}

func (s *LifecycleHookTestSuite) TestLifecycleHooksNoOpWithEmptyConfig() {
	ctx := context.Background()
	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	// Both functions should return nil with empty config (no commands to run)
	deferred, err := RunPreAttachHooks(ctx, result, false)
	assert.NoError(s.T(), err)
	assert.True(s.T(), deferred.Empty())

	err = RunPostAttachHooks(ctx, result)
	assert.NoError(s.T(), err)
}

func (s *LifecycleHookTestSuite) TestResolveLifecycleEnvIncludesSecrets() {
	t := s.T()

	// Set one secret in the environment, leave another absent.
	t.Setenv("SECRET_PRESENT", "s3cret")

	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				Secrets: map[string]config.SecretConfig{
					"SECRET_PRESENT": {Description: "a present secret"},
					"SECRET_ABSENT":  {Description: "a missing secret"},
				},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	env := resolveLifecycleEnv(context.Background(), result)

	assert.Equal(t, "s3cret", env.remoteEnv["SECRET_PRESENT"])
	_, found := env.remoteEnv["SECRET_ABSENT"]
	assert.False(t, found, "SECRET_ABSENT should not be in remoteEnv when not set in environment")
}

func (s *LifecycleHookTestSuite) TestResolveWaitForDefault() {
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor(""))
}

func (s *LifecycleHookTestSuite) TestResolveWaitForValid() {
	assert.Equal(s.T(), PhasePostCreate, resolveWaitFor("postCreateCommand"))
	assert.Equal(s.T(), PhasePostStart, resolveWaitFor("postStartCommand"))
	assert.Equal(s.T(), PhaseOnCreate, resolveWaitFor("onCreateCommand"))
	assert.Equal(s.T(), PhasePostAttach, resolveWaitFor("postAttachCommand"))
	assert.Equal(s.T(), PhaseUpdateContent, resolveWaitFor("updateContentCommand"))
}

func (s *LifecycleHookTestSuite) TestResolveWaitForInvalid() {
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor("bogus"))
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor("initializeCommand"))
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor("POSTCREATECOMMAND"))
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForDefaultSplit() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, DefaultWaitFor)
	assert.NoError(t, err)

	// Default waitFor is updateContentCommand.
	// Deferred should be postCreateCommand and postStartCommand.
	assert.Len(t, deferred, 2)
	assert.Equal(t, PhasePostCreate, deferred[0].phase)
	assert.Equal(t, PhasePostStart, deferred[1].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForPostCreate() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhasePostCreate)
	assert.NoError(t, err)

	// Only postStartCommand should be deferred.
	assert.Len(t, deferred, 1)
	assert.Equal(t, PhasePostStart, deferred[0].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForPostStart() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhasePostStart)
	assert.NoError(t, err)

	// All pre-attach hooks run in foreground, nothing deferred.
	assert.Empty(t, deferred)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForOnCreate() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhaseOnCreate)
	assert.NoError(t, err)

	// updateContentCommand, postCreateCommand, postStartCommand deferred.
	assert.Len(t, deferred, 3)
	assert.Equal(t, PhaseUpdateContent, deferred[0].phase)
	assert.Equal(t, PhasePostCreate, deferred[1].phase)
	assert.Equal(t, PhasePostStart, deferred[2].phase)
}

func (s *LifecycleHookTestSuite) TestPrebuildIgnoresWaitFor() {
	ctx := context.Background()
	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				// Set waitFor to onCreateCommand — prebuild should ignore this.
				WaitFor: "onCreateCommand",
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	// In prebuild mode, no deferred hooks are returned regardless of waitFor.
	deferred, err := RunPreAttachHooks(ctx, result, true)
	assert.NoError(s.T(), err)
	assert.True(s.T(), deferred.Empty())
}

func (s *LifecycleHookTestSuite) TestDeferredHooksEmpty() {
	d := DeferredHooks{}
	assert.True(s.T(), d.Empty())
	assert.NoError(s.T(), d.Run())
}

// makeTestPhaseHooks creates a phaseHook slice with no commands so that
// runHook is a no-op. This lets us test the split logic without executing
// real processes.
func makeTestPhaseHooks() []phaseHook {
	return []phaseHook{
		{phase: PhaseOnCreate, params: hookRunParams{name: "onCreateCommands"}},
		{phase: PhaseUpdateContent, params: hookRunParams{name: "updateContentCommands"}},
		{phase: PhasePostCreate, params: hookRunParams{name: "postCreateCommands"}},
		{phase: PhasePostStart, params: hookRunParams{name: "postStartCommands"}},
	}
}

func ptr(s string) *string { return &s }

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilUnsetsKey() {
	probedEnv := map[string]string{"KEEP": "yes", "DROP": "bye"}
	remoteEnv := map[string]*string{"DROP": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "yes", result["KEEP"])
	_, found := result["DROP"]
	assert.False(s.T(), found, "nil value should unset key")
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNonNilOverrides() {
	probedEnv := map[string]string{"VAR": "old"}
	remoteEnv := map[string]*string{"VAR": ptr("new")}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "new", result["VAR"])
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilMissingKeyNoop() {
	probedEnv := map[string]string{"OTHER": "val"}
	remoteEnv := map[string]*string{"ABSENT": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "val", result["OTHER"])
	_, found := result["ABSENT"]
	assert.False(s.T(), found)
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilPATHRemoves() {
	probedEnv := map[string]string{
		"PATH": "/usr/bin:/bin",
		"HOME": "/home/dev",
	}
	remoteEnv := map[string]*string{"PATH": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	_, found := result["PATH"]
	assert.False(s.T(), found, "nil PATH should remove PATH")
	assert.Equal(s.T(), "/home/dev", result["HOME"])
}

func TestLifecycleHookTestSuite(t *testing.T) {
	suite.Run(t, new(LifecycleHookTestSuite))
}

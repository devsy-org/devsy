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
	err := RunPreAttachHooks(ctx, result, false)
	assert.NoError(s.T(), err)

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

func TestLifecycleHookTestSuite(t *testing.T) {
	suite.Run(t, new(LifecycleHookTestSuite))
}

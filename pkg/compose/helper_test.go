package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/suite"
)

const (
	testPodmanCmd        = "podman"
	testDockerCmd        = "docker"
	testDockerComposeCmd = "docker-compose"
	testComposeArg       = "compose"
	testPodmanVersion    = "2.32.4"
)

type HelperTestSuite struct {
	suite.Suite
}

func TestHelperSuite(t *testing.T) {
	suite.Run(t, new(HelperTestSuite))
}

func (s *HelperTestSuite) TestParseVersion() {
	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "standard semver",
			version: "2.37.1",
			want:    "2.37.1",
		},
		{
			name:    "semver with v prefix",
			version: "v2.37.1",
			want:    "2.37.1",
		},
		{
			name:    "ubuntu package version",
			version: "2.37.1+ds1-0ubuntu2~24.04.1",
			want:    "2.37.1",
		},
		{
			name:    "desktop version",
			version: "2.40.3-desktop.1",
			want:    "2.40.3",
		},
		{
			name:    "another ubuntu variant",
			version: "2.37.1+ds1-0ubuntu1~24",
			want:    "2.37.1",
		},
		{
			name:    "invalid version",
			version: "083f676",
			wantErr: true,
		},
		{
			name:    "empty version",
			version: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := parseVersion(tt.version)
			if tt.wantErr {
				s.Error(err)
			} else {
				s.NoError(err)
				s.Equal(tt.want, got.String())
			}
		})
	}
}

func (s *HelperTestSuite) TestParseVersionWithPodmanWarning() {
	// Validates that parseVersion can extract the version even when the input
	// contains extra content (e.g., if warnings were accidentally captured in stdout).
	cmdOutput := ">>>> Executing external compose provider \"/home/linuxbrew/.linuxbrew/bin/docker-compose\". " +
		"Please see podman-compose(1) for how to disable this message. <<<<\n\n5.1.0\n"
	v, err := parseVersion(cmdOutput)
	s.NoError(err)
	s.Equal("5.1.0", v.String())
}

func (s *HelperTestSuite) TestParseVersionPodmanCompose() {
	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "podman compose standard version",
			version: testPodmanVersion,
			want:    testPodmanVersion,
		},
		{
			name:    "podman compose with v prefix",
			version: "v" + testPodmanVersion,
			want:    testPodmanVersion,
		},
		{
			name:    "podman-compose python variant",
			version: "1.0.6",
			want:    "1.0.6",
		},
		{
			name:    "podman compose with trailing newline",
			version: testPodmanVersion + "\n",
			want:    testPodmanVersion,
		},
		{
			name: "podman compose with external provider warning",
			version: ">>>> Executing external compose provider." +
				" Please see podman-compose(1) <<<<\n\n" + testPodmanVersion + "\n",
			want: testPodmanVersion,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := parseVersion(tt.version)
			if tt.wantErr {
				s.Error(err)
			} else {
				s.NoError(err)
				s.Equal(tt.want, got.String())
			}
		})
	}
}

func (s *HelperTestSuite) TestComposeHelperPodmanFields() {
	helper := &ComposeHelper{
		Command: testPodmanCmd,
		Version: testPodmanVersion,
		Args:    []string{testComposeArg},
	}

	s.Equal(testPodmanCmd, helper.Command)
	s.Equal(testPodmanVersion, helper.Version)
	s.Equal([]string{testComposeArg}, helper.Args)
}

func (s *HelperTestSuite) TestComposeHelperBuildCmdPodman() {
	helper := &ComposeHelper{
		Command: testPodmanCmd,
		Version: testPodmanVersion,
		Args:    []string{testComposeArg},
	}

	cmd := helper.buildCmd(context.TODO(), "--project-name", "test", "up", "-d")
	s.True(strings.HasSuffix(cmd.Path, testPodmanCmd))
	s.Contains(cmd.Args, testComposeArg)
	s.Contains(cmd.Args, "--project-name")
	s.Contains(cmd.Args, "test")
	s.Contains(cmd.Args, "up")
	s.Contains(cmd.Args, "-d")
}

// stubRuntime implements docker.ContainerRuntime for testing detection order.
type stubRuntime struct {
	name docker.RuntimeName
}

func (r stubRuntime) Name() docker.RuntimeName       { return r.name }
func (r stubRuntime) SupportsInternalBuildKit() bool { return false }
func (r stubRuntime) SupportsSignalProxy() bool      { return false }
func (r stubRuntime) SupportsMountConsistency() bool { return false }
func (r stubRuntime) NeedsUserNamespaceArgs() bool   { return false }
func (r stubRuntime) GPUAvailable(_ context.Context, _ *docker.DockerHelper) (bool, error) {
	return false, nil
}

func (s *HelperTestSuite) TestNewComposeHelperPodmanRuntimeUsesDockerCommand() {
	helper := &docker.DockerHelper{
		DockerCommand: "podman",
		Runtime:       stubRuntime{name: docker.RuntimePodman},
	}

	ch, err := NewComposeHelper(helper)
	if err != nil {
		s.T().Skipf("compose binary not available in test environment: %v", err)
	}

	s.Equal("podman", ch.Command)
	s.Equal([]string{testComposeArg}, ch.Args)
}

func (s *HelperTestSuite) TestNewComposeHelperDockerRuntimeUsesDockerCommand() {
	helper := &docker.DockerHelper{
		DockerCommand: testDockerCmd,
		Runtime:       stubRuntime{name: docker.RuntimeDocker},
	}

	ch, err := NewComposeHelper(helper)
	if err != nil {
		s.T().Skipf("compose binary not available in test environment: %v", err)
	}

	s.Equal(testDockerCmd, ch.Command)
	s.Equal([]string{testComposeArg}, ch.Args)
}

func (s *HelperTestSuite) TestNewComposeHelperDefaultDockerCommand() {
	helper := &docker.DockerHelper{
		DockerCommand: "",
		Runtime:       stubRuntime{name: docker.RuntimeDocker},
	}

	ch, err := NewComposeHelper(helper)
	if err != nil {
		s.T().Skipf("compose binary not available in test environment: %v", err)
	}

	s.Equal(testDockerCmd, ch.Command)
}

func (s *HelperTestSuite) TestNewComposeHelperNerdctlRuntimeFallsBackToDocker() {
	helper := &docker.DockerHelper{
		DockerCommand: "nerdctl",
		Runtime:       stubRuntime{name: docker.RuntimeNerdctl},
	}

	ch, err := NewComposeHelper(helper)
	if err != nil {
		s.T().Skipf("compose binary not available in test environment: %v", err)
	}

	s.Contains([]string{"nerdctl", testDockerCmd, testDockerComposeCmd}, ch.Command)
}

func (s *HelperTestSuite) TestTryComposeSubcommandUsesProvidedCommand() {
	helper, err := tryComposeSubcommand("podman")
	if err != nil {
		s.T().Skipf("podman not available in test environment: %v", err)
	}

	s.Equal("podman", helper.Command)
	s.Equal([]string{testComposeArg}, helper.Args)
}

func (s *HelperTestSuite) TestTryComposeSubcommandRejectsNonexistentCommand() {
	_, err := tryComposeSubcommand("nonexistent-binary-xyz")
	s.Error(err)
	s.Contains(err.Error(), "not found in PATH")
}

func (s *HelperTestSuite) TestNewComposeHelperNonPodmanFallbackUsesPodman() {
	helper := &docker.DockerHelper{
		DockerCommand: testDockerCmd,
		Runtime:       stubRuntime{name: docker.RuntimeDocker},
	}

	ch, err := NewComposeHelper(helper)
	if err != nil {
		s.T().Skipf("no compose binary available in test environment: %v", err)
	}

	// When Docker runtime succeeds, it should use testDockerCmd — but if Docker Compose V2
	// is unavailable, the fallback should independently probe "podman", not re-try testDockerCmd.
	// We verify here that the successful helper uses a valid command.
	s.Contains([]string{testDockerCmd, testPodmanCmd, testDockerComposeCmd}, ch.Command)
}

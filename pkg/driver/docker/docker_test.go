package docker

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	testSeccompUnconfined   = "seccomp=unconfined"
	testSecurityOptFlag     = "--security-opt"
	testBindMount           = "type=bind,src=/a,dst=/b"
	testUpdateUIDDefaultOff = "off"
	testUpdateUIDDefaultOn  = "on"
	testOSLinux             = "linux"
	testRemoteUser          = "vscode"
)

type DockerDriverTestSuite struct {
	suite.Suite
	driver *dockerDriver
}

func TestDockerDriverSuite(t *testing.T) {
	suite.Run(t, new(DockerDriverTestSuite))
}

func (s *DockerDriverTestSuite) SetupTest() {
	s.driver = &dockerDriver{}
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_RootContainerUser() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "0", Gid: "0"}

	result := shouldSkipUpdate(localUser, info)

	s.True(result, "should skip when container user is root")
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_MatchingUIDs() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "1000", Gid: "1000"}

	result := shouldSkipUpdate(localUser, info)

	s.True(result, "should skip when UIDs and GIDs match")
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_DifferentUIDs() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "1001", Gid: "1001"}

	result := shouldSkipUpdate(localUser, info)

	s.False(result, "should not skip when UIDs differ")
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_UIDMatch_GIDDifferent() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "1000", Gid: "1001"}

	result := shouldSkipUpdate(localUser, info)

	s.False(result, "should not skip when UID matches but GID differs")
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_UIDDifferent_GIDMatch() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "1001", Gid: "1000"}

	result := shouldSkipUpdate(localUser, info)

	s.False(result, "should not skip when GID matches but UID differs")
}

func (s *DockerDriverTestSuite) TestShouldSkipUpdate_RootWithDifferentGID() {
	localUser := &user.User{Uid: "1000", Gid: "1000"}
	info := &user.User{Uid: "0", Gid: "1001"}

	result := shouldSkipUpdate(localUser, info)

	s.True(result, "should skip when container user is root regardless of GID")
}

func (s *DockerDriverTestSuite) TestShouldUpdateUserUID_DefaultTrue_WhenConfigNil() {
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: testRemoteUser,
		},
	}
	s.driver.UpdateRemoteUserUIDDefault = ""
	result := s.driver.shouldUpdateUserUID(cfg)
	if runtime.GOOS == testOSLinux {
		s.True(result)
	}
}

func (s *DockerDriverTestSuite) TestShouldUpdateUserUID_CLIDefaultOff_WhenConfigNil() {
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: testRemoteUser,
		},
	}
	s.driver.UpdateRemoteUserUIDDefault = testUpdateUIDDefaultOff
	result := s.driver.shouldUpdateUserUID(cfg)
	s.False(result)
}

func (s *DockerDriverTestSuite) TestShouldUpdateUserUID_CLIDefaultOn_WhenConfigNil() {
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: testRemoteUser,
		},
	}
	s.driver.UpdateRemoteUserUIDDefault = testUpdateUIDDefaultOn
	result := s.driver.shouldUpdateUserUID(cfg)
	if runtime.GOOS == testOSLinux {
		s.True(result)
	}
}

func (s *DockerDriverTestSuite) TestShouldUpdateUserUID_ConfigTakesPrecedence_True() {
	t := true
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser:          testRemoteUser,
			UpdateRemoteUserUID: &t,
		},
	}
	s.driver.UpdateRemoteUserUIDDefault = testUpdateUIDDefaultOff
	result := s.driver.shouldUpdateUserUID(cfg)
	if runtime.GOOS == testOSLinux {
		s.True(result, "devcontainer.json true should override CLI default off")
	}
}

func (s *DockerDriverTestSuite) TestShouldUpdateUserUID_ConfigTakesPrecedence_False() {
	f := false
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser:          testRemoteUser,
			UpdateRemoteUserUID: &f,
		},
	}
	s.driver.UpdateRemoteUserUIDDefault = testUpdateUIDDefaultOn
	result := s.driver.shouldUpdateUserUID(cfg)
	s.False(result, "devcontainer.json false should override CLI default on")
}

func (s *DockerDriverTestSuite) TestGetContainerUser_RemoteUserPriority() {
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: "remote",
		},
		NonComposeBase: config.NonComposeBase{
			ContainerUser: "container",
		},
	}

	result := s.driver.getContainerUser(cfg)

	s.Equal("remote", result, "should prioritize RemoteUser")
}

func (s *DockerDriverTestSuite) TestGetContainerUser_ContainerUserFallback() {
	cfg := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			ContainerUser: "container",
		},
	}

	result := s.driver.getContainerUser(cfg)

	s.Equal("container", result, "should use ContainerUser when RemoteUser is empty")
}

func (s *DockerDriverTestSuite) TestGetContainerUser_BothEmpty() {
	cfg := &config.DevContainerConfig{}

	result := s.driver.getContainerUser(cfg)

	s.Equal("", result, "should return empty when both are empty")
}

func (s *DockerDriverTestSuite) TestGatherUpdateRequirements_WithRemoteUser() {
	cfg := &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			RemoteUser: "testuser",
		},
	}

	localUser, containerUser, err := s.driver.gatherUpdateRequirements(cfg)

	s.NoError(err)
	s.NotNil(localUser)
	s.Equal("testuser", containerUser)
}

func (s *DockerDriverTestSuite) TestGatherUpdateRequirements_WithContainerUser() {
	cfg := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			ContainerUser: "container",
		},
	}

	localUser, containerUser, err := s.driver.gatherUpdateRequirements(cfg)

	s.NoError(err)
	s.NotNil(localUser)
	s.Equal("container", containerUser)
}

func (s *DockerDriverTestSuite) TestAddCapabilityArgs_SingleSecurityOpt() {
	opts := &driver.RunOptions{SecurityOpt: []string{testSeccompUnconfined}}
	args := s.driver.addCapabilityArgs(nil, opts)
	s.Equal([]string{testSecurityOptFlag, testSeccompUnconfined}, args)
}

func (s *DockerDriverTestSuite) TestAddCapabilityArgs_MultipleSecurityOpts() {
	opts := &driver.RunOptions{
		SecurityOpt: []string{testSeccompUnconfined, "apparmor=unconfined"},
	}
	args := s.driver.addCapabilityArgs(nil, opts)
	s.Equal([]string{
		testSecurityOptFlag, testSeccompUnconfined,
		testSecurityOptFlag, "apparmor=unconfined",
	}, args)
}

func (s *DockerDriverTestSuite) TestAddCapabilityArgs_EmptySecurityOpt() {
	opts := &driver.RunOptions{}
	args := s.driver.addCapabilityArgs(nil, opts)
	s.Nil(args)
}

func (s *DockerDriverTestSuite) TestAddCapabilityArgs_CapAddAndSecurityOpt() {
	opts := &driver.RunOptions{
		CapAdd:      []string{"SYS_PTRACE"},
		SecurityOpt: []string{testSeccompUnconfined},
	}
	args := s.driver.addCapabilityArgs(nil, opts)
	s.Equal([]string{
		"--cap-add", "SYS_PTRACE",
		testSecurityOptFlag, testSeccompUnconfined,
	}, args)
}

func (s *DockerDriverTestSuite) TestIsLegacyDriverOwnedVolume() {
	cases := []struct {
		name string
		want bool
	}{
		{"dockerless-abc123", true},
		{"devsy-agent-abc123", true},
		{"node_modules-cache", false},
		{"my-cargo-cache", false},
		{"", false},
	}
	for _, tc := range cases {
		s.Equal(tc.want, isLegacyDriverOwnedVolume(tc.name), tc.name)
	}
}

func (s *DockerDriverTestSuite) TestClassifyVolumeRemoveError() {
	cases := []struct {
		msg  string
		want volumeErrAction
	}{
		{"Error response from daemon: volume is in use", volumeErrIgnore},
		{"Error: no such volume: foo", volumeErrIgnore},
		{"Error: No such volume: foo", volumeErrIgnore},
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock", volumeErrAbort},
		{"Is the docker daemon running?", volumeErrAbort},
		{
			"Got permission denied while trying to connect to the Docker daemon socket",
			volumeErrAbort,
		},
		{"permission denied: /var/run/docker.sock", volumeErrAbort},
		// Bare "permission denied" with no daemon hint must NOT abort —
		// one restricted volume shouldn't halt cleanup of the rest.
		{"Error: permission denied removing volume foo", volumeErrOther},
		{"permission denied", volumeErrOther},
		{"some other docker error", volumeErrOther},
	}
	for _, tc := range cases {
		got := classifyVolumeRemoveError(errors.New(tc.msg))
		s.Equal(tc.want, got, tc.msg)
	}
	s.Equal(volumeErrOther, classifyVolumeRemoveError(nil))
}

func (s *DockerDriverTestSuite) TestClassifyVolumeInspectError() {
	s.Equal(volumeErrOther, classifyVolumeInspectError(nil))
	s.Equal(
		volumeErrAbort,
		classifyVolumeInspectError(
			errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"),
		),
	)
	s.Equal(volumeErrOther, classifyVolumeInspectError(errors.New("some inspect failure")))
}

func (s *DockerDriverTestSuite) TestCollectNamedVolumes() {
	c := &config.ContainerDetails{
		Mounts: []config.ContainerMount{
			{Type: "volume", Source: "named-1"},
			{Type: "volume", Source: ""}, // anonymous, skipped
			{Type: "bind", Source: "/host"},
			{Type: "volume", Source: "named-2"},
		},
	}
	got := collectNamedVolumes(c)
	s.Equal([]string{"named-1", "named-2"}, got)
}

func writeScript(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	//nolint:gosec // test helper needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func (s *DockerDriverTestSuite) TestFilterDriverOwnedVolumes_LabelAndLegacy() {
	tmp := s.T().TempDir()
	// script branches on the volume name passed at end of args
	bin := writeScript(s.T(), tmp, "docker-fake", `#!/bin/sh
last=
for a in "$@"; do last=$a; done
case "$last" in
  labeled) echo '{"devsy.driver-owned":"true"}'; exit 0;;
  unlabeled) echo '{}'; exit 0;;
  bogus) echo 'Error: no such volume: bogus' 1>&2; exit 1;;
esac
`)

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	got, err := d.filterDriverOwnedVolumes(context.Background(), []string{
		"dockerless-xyz",  // legacy
		"devsy-agent-xyz", // legacy
		"labeled",         // labeled
		"unlabeled",       // not owned, user cache
		"bogus",           // missing -> excluded
	})
	s.NoError(err)
	s.Equal([]string{"dockerless-xyz", "devsy-agent-xyz", "labeled"}, got)
}

// TestFilterDriverOwnedVolumes_DaemonDownAborts verifies that when
// `docker volume inspect` reports the daemon is unreachable we surface
// the failure instead of silently treating every candidate as
// "not owned" and exiting cleanly.
func (s *DockerDriverTestSuite) TestFilterDriverOwnedVolumes_DaemonDownAborts() {
	tmp := s.T().TempDir()
	bin := writeScript(s.T(), tmp, "docker-fake", `#!/bin/sh
echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?' 1>&2
exit 1
`)

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	got, err := d.filterDriverOwnedVolumes(context.Background(), []string{"some-vol"})
	s.Error(err)
	s.Nil(got)
}

// TestRemoveContainerAndVolumes_DaemonDownReturnsSentinel ensures the
// daemon-down classification on volume removal produces an error that
// wraps ErrDockerDaemonUnavailable, rather than the previous silent
// `return nil` path that masked the outage from `devsy delete`.
func (s *DockerDriverTestSuite) TestRemoveContainerAndVolumes_DaemonDownReturnsSentinel() {
	tmp := s.T().TempDir()
	// First call ("rm -fv ...") succeeds; subsequent volume calls report
	// the daemon is unreachable. We sequence by writing a counter file.
	counter := filepath.Join(tmp, "counter")
	require.NoError(s.T(), os.WriteFile(counter, []byte("0"), 0o600))
	bin := writeScript(s.T(), tmp, "docker-fake", `#!/bin/sh
n=$(cat `+counter+`)
echo $((n+1)) > `+counter+`
if [ "$n" = "0" ]; then
  # container removal succeeds
  exit 0
fi
echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock' 1>&2
exit 1
`)

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	container := &config.ContainerDetails{
		ID: "c1",
		Mounts: []config.ContainerMount{
			// legacy name → skipped by inspect path, hits RemoveVolume directly
			{Type: "volume", Source: "dockerless-xyz"},
		},
	}
	err := d.removeContainerAndVolumes(context.Background(), container)
	s.Error(err)
	s.ErrorIs(err, ErrDockerDaemonUnavailable)
}

func (s *DockerDriverTestSuite) TestStripMountConsistency() {
	tests := []struct {
		input string
		want  string
	}{
		{testBindMount + ",consistency='consistent'", testBindMount},
		{testBindMount + ",consistency=delegated", testBindMount},
		{testBindMount, testBindMount},
	}
	for _, tt := range tests {
		s.Equal(tt.want, stripMountConsistency(tt.input))
	}
}

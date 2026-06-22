package docker

import (
	"os/user"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
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
	testRunArg              = "run"
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

func (s *DockerDriverTestSuite) TestWithBindCreateSrc() {
	existing := s.T().TempDir()

	// Source exists: bind-create-src is appended to bust a stale file-share
	// inode while still binding the real (present) directory.
	withExisting := "type=bind,src=" + existing + ",dst=/b"
	s.Equal(withExisting+",bind-create-src=true", withBindCreateSrc(withExisting))

	// Source missing: spec is left untouched so docker fails loudly instead of
	// silently materializing an empty placeholder directory.
	s.Equal(testBindMount, withBindCreateSrc(testBindMount))

	// Idempotent: an existing bind-create-src is not duplicated.
	already := withExisting + ",bind-create-src=true"
	s.Equal(already, withBindCreateSrc(already))

	// Non-bind mounts are ignored.
	vol := "type=volume,src=myvol,dst=/b"
	s.Equal(vol, withBindCreateSrc(vol))
}

func (s *DockerDriverTestSuite) TestAddRunPlatform_SetAppendsFlag() {
	b := &runArgsBuilder{
		args:   []string{testRunArg},
		driver: s.driver,
		params: &driver.RunDockerDevContainerParams{
			Options:      &driver.RunOptions{Platform: "linux/amd64"},
			ParsedConfig: &config.DevContainerConfig{},
		},
	}
	b.addRunPlatform()
	s.Contains(b.args, "--platform=linux/amd64")
}

func (s *DockerDriverTestSuite) TestAddRunPlatform_EmptyNoFlag() {
	b := &runArgsBuilder{
		args:   []string{testRunArg},
		driver: s.driver,
		params: &driver.RunDockerDevContainerParams{
			Options:      &driver.RunOptions{Platform: ""},
			ParsedConfig: &config.DevContainerConfig{},
		},
	}
	b.addRunPlatform()
	for _, a := range b.args {
		s.NotContains(a, "--platform")
	}
}

func (s *DockerDriverTestSuite) TestAddRunPlatform_ExplicitInConfigNotDuplicated() {
	b := &runArgsBuilder{
		args:   []string{testRunArg},
		driver: s.driver,
		params: &driver.RunDockerDevContainerParams{
			Options: &driver.RunOptions{Platform: "linux/amd64"},
			ParsedConfig: &config.DevContainerConfig{
				NonComposeBase: config.NonComposeBase{
					RunArgs: []string{"--platform", "linux/arm64"},
				},
			},
		},
	}
	b.addRunPlatform()
	count := 0
	for _, a := range b.args {
		if a == "--platform=linux/amd64" {
			count++
		}
	}
	s.Equal(0, count, "should not auto-add when config already sets --platform")
}

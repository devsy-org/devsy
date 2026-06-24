package docker

import (
	"os/user"
	"runtime"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

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
			ContainerUser: testContainerUser,
		},
	}

	result := s.driver.getContainerUser(cfg)

	s.Equal("remote", result, "should prioritize RemoteUser")
}

func (s *DockerDriverTestSuite) TestGetContainerUser_ContainerUserFallback() {
	cfg := &config.DevContainerConfig{
		NonComposeBase: config.NonComposeBase{
			ContainerUser: testContainerUser,
		},
	}

	result := s.driver.getContainerUser(cfg)

	s.Equal(testContainerUser, result, "should use ContainerUser when RemoteUser is empty")
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
			ContainerUser: testContainerUser,
		},
	}

	localUser, containerUser, err := s.driver.gatherUpdateRequirements(cfg)

	s.NoError(err)
	s.NotNil(localUser)
	s.Equal(testContainerUser, containerUser)
}

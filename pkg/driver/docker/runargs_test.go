package docker

import (
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

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

	// source exists -> option appended
	withExisting := "type=bind,src=" + existing + ",dst=/b"
	s.Equal(withExisting+",bind-create-src=true", withBindCreateSrc(withExisting))

	// source missing -> untouched
	s.Equal(testBindMount, withBindCreateSrc(testBindMount))

	// idempotent
	already := withExisting + ",bind-create-src=true"
	s.Equal(already, withBindCreateSrc(already))

	// non-bind ignored
	vol := "type=volume,src=myvol,dst=/b"
	s.Equal(vol, withBindCreateSrc(vol))
}

func (s *DockerDriverTestSuite) TestDockerMajorAtLeast() {
	tests := []struct {
		version string
		want    bool
	}{
		{"29.5.3", true},
		{"29.0.0", true},
		{"30.1.0", true},
		{"28.0.4", false},
		{"20.10.21", false},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		s.Equalf(tt.want, dockerMajorAtLeast(tt.version, minBindCreateSrcMajor),
			"dockerMajorAtLeast(%q)", tt.version)
	}
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
	_ = b.addRunPlatform()
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
	_ = b.addRunPlatform()
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
	_ = b.addRunPlatform()
	count := 0
	for _, a := range b.args {
		if a == "--platform=linux/amd64" {
			count++
		}
	}
	s.Equal(0, count, "should not auto-add when config already sets --platform")
}

package docker

import (
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

const (
	testBindMountType = "bind"
	testReadonly      = "readonly"
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

func (s *DockerDriverTestSuite) TestDockerClientSupportsBindCreateSrc() {
	tests := []struct {
		version string
		want    bool
	}{
		{"28.5.2", false},
		{"29.0.0", false},
		{"29.1.0", false},
		{"29.2.1", false},
		{"29.2.99", false},
		{"29.3.0", true},
		{"29.3.1", true},
		{"29.7.2", true},
		{"30.0.0", true},
		{"v29.3.0", true},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		s.Equalf(tt.want, dockerClientSupportsBindCreateSrc(tt.version),
			"dockerClientSupportsBindCreateSrc(%q)", tt.version)
	}
}

func (s *DockerDriverTestSuite) TestAddRunPlatform_SetAppendsFlag() {
	b := &runArgsBuilder{
		args:   []string{testRunArg},
		driver: s.driver,
		params: &driver.RunImageDevContainerParams{
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
		params: &driver.RunImageDevContainerParams{
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
		params: &driver.RunImageDevContainerParams{
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

func (s *DockerDriverTestSuite) TestAddMountArgs_EngineOnlyBindSource() {
	source := filepath.Join(s.T().TempDir(), "engine-only.sock")
	opts := &driver.RunOptions{
		Mounts: []*config.Mount{
			{
				Type:   testBindMountType,
				Source: source,
				Target: "/run/engine.sock",
				Other:  []string{testReadonly},
			},
		},
	}
	args := s.driver.addMountArgs([]string{testRunArg}, opts)
	s.Equal(
		[]string{
			testRunArg,
			mountFlag,
			"type=bind,src=" + source + ",dst=/run/engine.sock,readonly",
		},
		args,
	)
	_, err := os.Lstat(source)
	s.True(os.IsNotExist(err))
}

func (s *DockerDriverTestSuite) TestAddMountArgs_PreservesAllMountTypesAndOrder() {
	existing := s.T().TempDir()
	absent := filepath.Join(existing, "absent")
	opts := &driver.RunOptions{Mounts: []*config.Mount{
		{
			Type:   testBindMountType,
			Source: existing,
			Target: "/existing",
			Other:  []string{testReadonly, "bind-propagation=rslave"},
		},
		{Type: testBindMountType, Source: absent, Target: "/absent"},
		{Type: "volume", Source: "data", Target: "/data", Other: []string{"volume-nocopy"}},
		{Type: "tmpfs", Target: "/tmp", Other: []string{"tmpfs-size=4096"}},
	}}
	args := s.driver.addMountArgs([]string{testRunArg}, opts)
	s.Equal([]string{
		testRunArg,
		mountFlag, "type=bind,src=" + existing + ",dst=/existing,readonly,bind-propagation=rslave",
		mountFlag, "type=bind,src=" + absent + ",dst=/absent",
		mountFlag, "type=volume,src=data,dst=/data,volume-nocopy",
		mountFlag, "type=tmpfs,dst=/tmp,tmpfs-size=4096",
	}, args)
}

func (s *DockerDriverTestSuite) TestAddMountArgs_DoesNotModifySourceOrOptions() {
	opts := &driver.RunOptions{Mounts: []*config.Mount{
		{
			Type:   testBindMountType,
			Source: filepath.Join(s.T().TempDir(), "engine.sock"),
			Target: "/socket",
			Other:  []string{testReadonly, "consistency=cached", "bind-propagation=rprivate"},
		},
		{
			Type:   testBindMountType,
			Source: "./engine/../sock",
			Target: "/relative",
			Other:  []string{testReadonly},
		},
		{
			Type:     "volume",
			Source:   "data",
			Target:   "/data",
			External: true,
			Other:    []string{"volume-nocopy"},
		},
		{Type: "tmpfs", Target: "/tmp", Other: []string{"tmpfs-size=4096"}},
	}}
	before := make([]config.Mount, len(opts.Mounts))
	formatted := make([]string, len(opts.Mounts))
	pointers := append([]*config.Mount(nil), opts.Mounts...)
	expected := []string{testRunArg}
	for i, mount := range opts.Mounts {
		before[i] = *mount
		before[i].Other = append([]string(nil), mount.Other...)
		formatted[i] = mount.String()
		expected = append(expected, mountFlag, formatted[i])
	}
	args := s.driver.addMountArgs([]string{testRunArg}, opts)
	s.Equal(expected, args)
	for i, mount := range opts.Mounts {
		s.Equal(before[i], *mount)
		s.Equal(formatted[i], mount.String())
		s.Same(pointers[i], mount)
	}
}

func (s *DockerDriverTestSuite) TestRunArgsBuilder_AdditionalMountReachesArgs() {
	source := filepath.Join(s.T().TempDir(), "engine.sock")
	b := &runArgsBuilder{
		args:   []string{testRunArg, "--init"},
		driver: s.driver,
		params: &driver.RunImageDevContainerParams{
			Options: &driver.RunOptions{
				Mounts: []*config.Mount{
					{
						Type:   testBindMountType,
						Source: source,
						Target: "/socket",
						Other:  []string{testReadonly},
					},
				},
			},
		},
	}
	s.Require().NoError(b.addMounts())
	s.Equal(
		[]string{
			testRunArg,
			"--init",
			mountFlag,
			"type=bind,src=" + source + ",dst=/socket,readonly",
		},
		b.args,
	)
}

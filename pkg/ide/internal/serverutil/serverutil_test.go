package serverutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/stretchr/testify/suite"
)

const (
	testAmd64Option = "AMD"
	testArm64Option = "ARM"
	cmdTrue         = "true"
	cmdFalse        = "false"
	goosWindows     = "windows"
)

type ServerUtilSuite struct {
	suite.Suite
}

func TestServerUtilSuite(t *testing.T) {
	suite.Run(t, new(ServerUtilSuite))
}

func (s *ServerUtilSuite) TestSuOrShWithoutUserUsesSh() {
	cmd := SuOrSh("", "echo hi", "")
	s.Equal([]string{"sh", "-c", "echo hi"}, cmd.Args)
	s.Empty(cmd.Dir)
}

func (s *ServerUtilSuite) TestSuOrShWithUserUsesSuAndSetsDir() {
	cmd := SuOrSh("alice", "echo hi", "/tmp/work")
	s.Equal([]string{"su", "alice", "-c", "echo hi"}, cmd.Args)
	s.Equal("/tmp/work", cmd.Dir)
}

func (s *ServerUtilSuite) TestReleaseSpecURLSelectsByGoarch() {
	spec := ReleaseSpec{
		Options: ide.Options{
			testAmd64Option: {Name: testAmd64Option},
			testArm64Option: {Name: testArm64Option},
		},
		Amd64Option:   testAmd64Option,
		Arm64Option:   testArm64Option,
		Amd64Template: "amd-%s-%s",
		Arm64Template: "arm-%s-%s",
	}

	s.Equal("amd-1-2", spec.URL(nil, "amd64", "1", "2"))
	s.Equal("arm-1-2", spec.URL(nil, "arm64", "1", "2"))
	s.Equal("amd-1-2", spec.URL(nil, "riscv64", "1", "2"))
}

func (s *ServerUtilSuite) TestReleaseSpecOverrideAppliesOnlyToItsArch() {
	spec := ReleaseSpec{
		Options: ide.Options{
			testAmd64Option: {Name: testAmd64Option},
			testArm64Option: {Name: testArm64Option},
		},
		Amd64Option:   testAmd64Option,
		Arm64Option:   testArm64Option,
		Amd64Template: "amd-%s",
		Arm64Template: "arm-%s",
	}
	values := map[string]config.OptionValue{
		testAmd64Option: {Value: "https://override/amd"},
	}

	s.Equal("https://override/amd", spec.URL(values, "amd64", "1"))
	s.Equal("arm-1", spec.URL(values, "arm64", "1"))
}

func (s *ServerUtilSuite) TestPrepareLocationCreatesDirUnderHome() {
	home := s.T().TempDir()
	s.T().Setenv("HOME", home)
	s.T().Setenv("USERPROFILE", home)

	location, err := PrepareLocation("", ".example-server")
	s.Require().NoError(err)
	s.Equal(filepath.Join(home, ".example-server"), location)

	info, err := os.Stat(location)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *ServerUtilSuite) TestChownIfNeededNoopWithoutUser() {
	s.NoError(ChownIfNeeded("", filepath.Join(s.T().TempDir(), "missing")))
}

func (s *ServerUtilSuite) TestExtensionInstallEmptyListSkipsCommand() {
	called := false
	err := ExtensionInstall{
		Extensions: nil,
		Command: func(string) string {
			called = true
			return cmdFalse
		},
		FailOnTotalFailure: true,
	}.Run()
	s.NoError(err)
	s.False(called)
}

func (s *ServerUtilSuite) TestExtensionInstallAllFailReturnsErrorWhenRequired() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("uses sh")
	}
	err := ExtensionInstall{
		Extensions:         []string{"a", "b"},
		Command:            func(string) string { return cmdFalse },
		FailOnTotalFailure: true,
	}.Run()
	s.Require().Error(err)
	s.Contains(err.Error(), "all 2 extensions failed to install")
}

func (s *ServerUtilSuite) TestExtensionInstallAllFailToleratedWhenNotRequired() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("uses sh")
	}
	err := ExtensionInstall{
		Extensions: []string{"a", "b"},
		Command:    func(string) string { return cmdFalse },
	}.Run()
	s.NoError(err)
}

func (s *ServerUtilSuite) TestExtensionInstallPartialFailureTolerated() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("uses sh")
	}
	err := ExtensionInstall{
		Extensions: []string{"ok", "bad"},
		Command: func(name string) string {
			if name == "ok" {
				return cmdTrue
			}
			return cmdFalse
		},
		FailOnTotalFailure: true,
	}.Run()
	s.NoError(err)
}

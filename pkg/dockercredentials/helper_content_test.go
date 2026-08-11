package dockercredentials

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type HelperContentTestSuite struct {
	suite.Suite
}

func TestHelperContentSuite(t *testing.T) {
	suite.Run(t, new(HelperContentTestSuite))
}

func (s *HelperContentTestSuite) TestBuildHelperContentUnix() {
	if runtime.GOOS == windowsOS {
		s.T().Skip("unix variant only on non-windows host")
	}

	const binaryPath = "/usr/local/bin/devsy"
	out := buildHelperContent(binaryPath, "#!/bin/sh", 4242)
	content := string(out)

	s.True(strings.HasPrefix(content, "#!/bin/sh\n"), "shebang on its own line")
	s.Contains(content, binaryPath)
	s.Contains(content, "internal agent docker-credentials")
	s.Contains(content, "--port 4242")
	s.Contains(content, `"$@"`)
	s.True(strings.HasSuffix(content, "\n"), "script must end with newline")
	s.NotContains(content, "\r", "unix script uses LF line endings")
}

func (s *HelperContentTestSuite) TestBuildHelperContentWindows() {
	if runtime.GOOS != windowsOS {
		s.T().Skip("windows variant only on windows host")
	}

	const binaryPath = `C:\Program Files\devsy\devsy.exe`
	out := buildHelperContent(binaryPath, "@echo off", 9999)
	content := string(out)

	s.True(strings.HasPrefix(content, "@echo off\r\n"))
	s.Contains(content, `"`+binaryPath+`"`)
	s.Contains(content, "internal agent docker-credentials")
	s.Contains(content, "--port 9999")
	s.Contains(content, "%*")
	s.True(strings.HasSuffix(content, "\r\n"), "script must end with CRLF")
}

func (s *HelperContentTestSuite) TestBuildHelperContentQuotesSpacesInBinaryPath() {
	if runtime.GOOS == windowsOS {
		s.T().Skip("unix quoting variant only on non-windows host")
	}

	const binaryPath = "/usr/local/bin/custom devsy"
	out := buildHelperContent(binaryPath, "#!/bin/sh", 1)
	content := string(out)

	s.Contains(content, `'/usr/local/bin/custom devsy'`)
	s.NotContains(content, binaryPath+" internal")
}

func (s *HelperContentTestSuite) TestBuildHelperContentEscapesWindowsPercent() {
	if runtime.GOOS != windowsOS {
		s.T().Skip("windows percent-escaping only on windows host")
	}

	const binaryPath = `C:\apps\devsy 1.0\devsy.exe`
	out := buildHelperContent(binaryPath, "@echo off", 7)
	content := string(out)

	s.Contains(content, `"`+binaryPath+`"`)
	s.Contains(content, "--port 7 %*")
}

func (s *HelperContentTestSuite) TestBuildHelperContentDoublesWindowsPercentInPath() {
	if runtime.GOOS != windowsOS {
		s.T().Skip("windows percent-doubling only on windows host")
	}

	const binaryPath = `C:\%USERNAME%\devsy.exe`
	out := buildHelperContent(binaryPath, "@echo off", 7)
	content := string(out)

	s.Contains(content, `"`+strings.ReplaceAll(binaryPath, "%", "%%")+`"`)
	s.NotContains(content, `"%USERNAME%`)
}

func (s *HelperContentTestSuite) TestBuildHelperContentEmbedsPort() {
	for _, port := range []int{0, 1, 65535} {
		out := buildHelperContent("/usr/local/bin/devsy", "#!/bin/sh", port)
		s.Contains(string(out), strconv.Itoa(port))
	}
}

func (s *HelperContentTestSuite) TestNewDockerCredentialsDirStructure() {
	const suffixLen = 12
	dir := newDockerCredentialsDir("/tmp/parent")

	s.Equal("/tmp/parent/docker-credentials-", dir[:len("/tmp/parent/docker-credentials-")])
	s.Len(dir, len("/tmp/parent/docker-credentials-")+suffixLen)
}

func (s *HelperContentTestSuite) TestAppendToPathAddsDir() {
	orig := os.Getenv("PATH")
	s.T().Cleanup(func() { _ = os.Setenv("PATH", orig) })

	s.NoError(os.Setenv("PATH", "/usr/bin"))
	s.NoError(appendToPath("/opt/bin"))

	parts := strings.Split(os.Getenv("PATH"), ":")
	s.Equal([]string{"/usr/bin", "/opt/bin"}, parts)
}

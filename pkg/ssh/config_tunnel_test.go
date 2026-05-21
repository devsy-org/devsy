package ssh

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

const (
	testUser          = "testuser"
	testUserRoot      = "root"
	testContext       = "default"
	testProvider      = "docker"
	testHost          = "my-workspace.devsy"
	testWorkspace     = "my-workspace"
	testWorkspaceShrt = "test"
	testDevsyBin      = "/usr/local/bin/devsy"
	testExecPath      = "/path/to/exec"
	testHostSimple    = "test.devsy"
	testHostBasic     = "testhost"
	testContextAlt    = "testcontext"
	testWorkspaceAlt  = "testworkspace"
	testTunnelPort    = 10800
	testTunnelPortStr = "Port 10800"
	testHostnameLocal = "Hostname 127.0.0.1"
	testProxyCommand  = "ProxyCommand"
)

type SSHConfigTunnelTestSuite struct {
	suite.Suite
}

func TestSSHConfigTunnelSuite(t *testing.T) {
	suite.Run(t, new(SSHConfigTunnelTestSuite))
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_TunnelMode_Basic() {
	params := addHostParams{
		host:       testHost,
		user:       testUser,
		context:    testContext,
		workspace:  testWorkspace,
		tunnelPort: testTunnelPort,
		provider:   testProvider,
	}

	result, err := addHostSection("", testDevsyBin, params)
	assert.NoError(s.T(), err)

	assert.Contains(s.T(), result, testHostnameLocal)
	assert.Contains(s.T(), result, testTunnelPortStr)
	assert.NotContains(s.T(), result, testProxyCommand)
	assert.Contains(s.T(), result, "ForwardAgent yes")
	assert.Contains(s.T(), result, "LogLevel error")
	assert.Contains(s.T(), result, "StrictHostKeyChecking no")
	assert.Contains(s.T(), result, "UserKnownHostsFile /dev/null")
	assert.Contains(s.T(), result, "HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa")
	assert.Contains(s.T(), result, MarkerStartPrefix+testHost)
	assert.Contains(s.T(), result, MarkerEndPrefix+testHost)
	assert.Contains(s.T(), result, "User "+testUser)
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_TunnelMode_DifferentPort() {
	params := addHostParams{
		host:       testHostSimple,
		user:       testUserRoot,
		context:    testContext,
		workspace:  testWorkspaceShrt,
		tunnelPort: 12345,
		provider:   testProvider,
	}

	result, err := addHostSection("", testDevsyBin, params)
	assert.NoError(s.T(), err)

	assert.Contains(s.T(), result, testHostnameLocal)
	assert.Contains(s.T(), result, "Port 12345")
	assert.NotContains(s.T(), result, testProxyCommand)
	assert.Contains(s.T(), result, "User "+testUserRoot)
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_TunnelMode_AWSProvider() {
	params := addHostParams{
		host:       testHostSimple,
		user:       testUser,
		context:    testContext,
		workspace:  testWorkspaceShrt,
		tunnelPort: testTunnelPort,
		provider:   "aws",
	}

	result, err := addHostSection("", testDevsyBin, params)
	assert.NoError(s.T(), err)

	assert.Contains(s.T(), result, "ConnectTimeout 60")
	assert.Contains(s.T(), result, testHostnameLocal)
	assert.Contains(s.T(), result, testTunnelPortStr)
	assert.NotContains(s.T(), result, testProxyCommand)
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_ProxyCommandMode_NoTunnel() {
	params := addHostParams{
		host:      testHostSimple,
		user:      testUserRoot,
		context:   testContext,
		workspace: testWorkspaceShrt,
		provider:  testProvider,
	}

	result, err := addHostSection("", testExecPath, params)
	assert.NoError(s.T(), err)

	assert.Contains(s.T(), result, testProxyCommand)
	assert.NotContains(s.T(), result, testHostnameLocal)
	assert.NotContains(s.T(), result, "Port ")
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_TunnelMode_ExistingConfig() {
	existingConfig := `Host existinghost
  User existinguser`

	params := addHostParams{
		host:       testHostSimple,
		user:       testUser,
		context:    testContext,
		workspace:  testWorkspaceShrt,
		tunnelPort: testTunnelPort,
		provider:   testProvider,
	}

	result, err := addHostSection(existingConfig, testDevsyBin, params)
	assert.NoError(s.T(), err)

	assert.Contains(s.T(), result, testHostnameLocal)
	assert.Contains(s.T(), result, testTunnelPortStr)
	assert.NotContains(s.T(), result, testProxyCommand)
	assert.Contains(s.T(), result, existingConfig)
}

func (s *SSHConfigTunnelTestSuite) TestAddHostSection_TunnelMode_FullExpectedOutput() {
	params := addHostParams{
		host:       testHostBasic,
		user:       testUser,
		context:    testContextAlt,
		workspace:  testWorkspaceAlt,
		tunnelPort: testTunnelPort,
		provider:   "",
	}

	result, err := addHostSection("", testExecPath, params)
	assert.NoError(s.T(), err)

	expected := `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  Hostname 127.0.0.1
  Port 10800
  User testuser
# Devsy End testhost`

	assert.Equal(s.T(), expected, result)
}

func (s *SSHConfigTunnelTestSuite) TestBuildTunnelConfigLines() {
	params := addHostParams{
		host:       testHost,
		user:       testUser,
		context:    testContext,
		workspace:  testWorkspace,
		tunnelPort: testTunnelPort,
		provider:   testProvider,
	}

	lines := buildTunnelConfigLines(params)
	config := strings.Join(lines, "\n")

	assert.Contains(s.T(), config, testHostnameLocal)
	assert.Contains(s.T(), config, testTunnelPortStr)
	assert.NotContains(s.T(), config, testProxyCommand)
	assert.Contains(s.T(), config, "ForwardAgent yes")
	assert.Contains(s.T(), config, "StrictHostKeyChecking no")
	assert.Contains(s.T(), config, MarkerStartPrefix+testHost)
	assert.Contains(s.T(), config, MarkerEndPrefix+testHost)
	assert.Contains(s.T(), config, "User "+testUser)
}

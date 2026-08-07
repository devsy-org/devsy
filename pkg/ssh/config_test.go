package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SSHConfigTestSuite struct {
	suite.Suite
}

func TestSSHConfigSuite(t *testing.T) {
	suite.Run(t, new(SSHConfigTestSuite))
}

var addHostSectionTestCases = []struct {
	name            string
	config          string
	execPath        string
	host            string
	user            string
	context         string
	workspace       string
	workdir         string
	command         string
	gpgagent        bool
	agentForwarding bool
	devsyHome       string
	provider        string
	expected        string
}{
	{
		name:            "Basic host addition",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "AWS provider with ConnectTimeout",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "aws",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ConnectTimeout 60
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "Basic host addition with DEVSY_HOME",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "C:\\\\W S\\d",
		provider:        "",
		//nolint:lll // long ProxyCommand expected output
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace --home "C:\\W S\d"
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "Host addition with workdir",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "/path/to/workdir",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		//nolint:lll // long ProxyCommand expected output
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace --workdir "/path/to/workdir"
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "Host addition with gpg agent",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        true,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		//nolint:lll // long ProxyCommand expected output
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace --ssh-gpg-forwarding
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "Host addition with custom command",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "ssh -W %h:%p bastion",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "ssh -W %h:%p bastion"
  User testuser
# Devsy End testhost`,
	},
	{
		name: "Host addition to existing config",
		config: `Host existinghost
  User existinguser`,
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost
Host existinghost
  User existinguser`,
	},
	{
		name: "Host addition to existing config with Devsy host",
		config: `# Devsy Start existingtesthost
Host existingtesthost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost

Host existinghost
  User existinguser`,
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost
# Devsy Start existingtesthost
Host existingtesthost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost

Host existinghost
  User existinguser`,
	},
	{
		name: "Host addition after top level includes",
		config: `Include ~/config1

Include ~/config2



Include ~/config3`,
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: true,
		devsyHome:       "",
		provider:        "",
		expected: `Include ~/config1

Include ~/config2



Include ~/config3
# Devsy Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost`,
	},
	{
		name:            "Host addition with agent forwarding disabled",
		config:          "",
		execPath:        testExecPath,
		host:            testHostBasic,
		user:            testUser,
		context:         testContextAlt,
		workspace:       testWorkspaceAlt,
		workdir:         "",
		command:         "",
		gpgagent:        false,
		agentForwarding: false,
		devsyHome:       "",
		provider:        "",
		expected: `# Devsy Start testhost
Host testhost
  ForwardAgent no
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" workspace ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# Devsy End testhost`,
	},
}

func (s *SSHConfigTestSuite) TestAddHostSection() {
	for _, tt := range addHostSectionTestCases {
		s.Run(tt.name, func() {
			result, err := addHostSection(tt.config, tt.execPath, addHostParams{
				path:            "",
				host:            tt.host,
				user:            tt.user,
				context:         tt.context,
				workspace:       tt.workspace,
				workdir:         tt.workdir,
				command:         tt.command,
				gpgagent:        tt.gpgagent,
				agentForwarding: tt.agentForwarding,
				devsyHome:       tt.devsyHome,
				provider:        tt.provider,
			})

			assert.NoError(s.T(), err)
			assert.Equal(s.T(), tt.expected, result)
			assert.Contains(s.T(), result, MarkerEndPrefix+tt.host)
			assert.Contains(s.T(), result, "Host "+tt.host)
			assert.Contains(s.T(), result, "User "+tt.user)

			if tt.command != "" {
				assert.Contains(s.T(), result, "ProxyCommand \""+tt.command+"\"")
			}

			if tt.workdir != "" {
				assert.Contains(s.T(), result, "--workdir \""+tt.workdir+"\"")
			}

			if tt.gpgagent {
				assert.Contains(s.T(), result, "--ssh-gpg-forwarding")
			}

			if tt.config != "" {
				assert.Contains(s.T(), result, tt.config)
			}
		})
	}
}

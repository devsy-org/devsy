package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
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
# Devsy End existingtesthost

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
# Devsy End existingtesthost

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

func TestNormalizeSSHExecPathForOS(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		input    string
		expected string
	}{
		{
			name:     "windows path",
			goos:     windowsGOOS,
			input:    `C:\Users\test\AppData\Local\Programs\Devsy\devsy.exe`,
			expected: `C:/Users/test/AppData/Local/Programs/Devsy/devsy.exe`,
		},
		{
			name:     "windows path with spaces",
			goos:     windowsGOOS,
			input:    `C:\Users\Test User\AppData\Local\Programs\Devsy\devsy.exe`,
			expected: `C:/Users/Test User/AppData/Local/Programs/Devsy/devsy.exe`,
		},
		{
			name:     "linux path",
			goos:     "linux",
			input:    `/usr/local/bin/devsy`,
			expected: `/usr/local/bin/devsy`,
		},
		{
			name:     "macos path",
			goos:     "darwin",
			input:    `/Applications/Devsy.app/Contents/MacOS/devsy`,
			expected: `/Applications/Devsy.app/Contents/MacOS/devsy`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeSSHExecPathForOS(tt.input, tt.goos))
		})
	}
}

//nolint:goconst // repeated values document distinct lexical syntax cases.
func TestSSHConfigKeyword(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{name: "empty", line: "", expected: ""},
		{name: "comment", line: "# comment", expected: ""},
		{name: "indented comment", line: "  # indented comment", expected: ""},
		{name: "host", line: "Host myserver", expected: "Host"},
		{name: "lowercase host", line: "host myserver", expected: "host"},
		{name: "uppercase host", line: "HOST myserver", expected: "HOST"},
		{name: "tab separator", line: "HoSt\tmyserver", expected: "HoSt"},
		{name: "equals separator", line: "Host=myserver", expected: "Host"},
		{name: "equals with spaces", line: "Host = myserver", expected: "Host"},
		{name: "hostname", line: "HostName example.com", expected: "HostName"},
		{
			name:     "host key algorithms",
			line:     "HostKeyAlgorithms ssh-ed25519",
			expected: "HostKeyAlgorithms",
		},
		{name: "match", line: `Match exec "true"`, expected: "Match"},
		{name: "lowercase match", line: "match host foo", expected: "match"},
		{name: "port", line: "Port 22", expected: "Port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, sshConfigKeyword(tt.line))
		})
	}
}

func TestIsSSHSectionStart(t *testing.T) {
	positives := []string{
		"Host myserver",
		"host myserver",
		"HOST myserver",
		"HoSt\tmyserver",
		"Host=myserver",
		"Host = myserver",
		`Match exec "true"`,
		"match host foo",
		"MATCH all",
	}
	for _, line := range positives {
		t.Run("positive/"+line, func(t *testing.T) {
			assert.True(t, isSSHSectionStart(line))
		})
	}

	negatives := []string{
		"HostName example.com",
		"hostname example.com",
		"HostKeyAlgorithms ssh-ed25519",
		"Port 22",
		"# Host commented",
		"# Match commented",
		"",
	}
	for _, line := range negatives {
		t.Run("negative/"+line, func(t *testing.T) {
			assert.False(t, isSSHSectionStart(line))
		})
	}
}

//nolint:funlen // keep the insertion-position regression matrix together.
func TestFindInsertPosition(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		expected int
	}{
		{
			name: "HostName is not a section",
			config: `HostName global.example.com
Host actual-host
    User example`,
			expected: 1,
		},
		{
			name: "lowercase host",
			config: `host *
    User alice

host server
    Port 22`,
			expected: 0,
		},
		{name: "mixed-case section", config: "HoSt server\n    User alice", expected: 0},
		{name: "tab separator", config: "Host\tserver", expected: 0},
		{name: "equals separator", config: "Host=server", expected: 0},
		{
			name: "Match",
			config: `IdentityFile ~/.ssh/id_ed25519

Match exec "true"
    User conditional`,
			expected: 2,
		},
		{
			name: "preceding comments stay attached",
			config: `# Production server
# Do not remove
Host prod
    User deploy`,
			expected: 0,
		},
		{
			name: "managed block stays contiguous",
			config: `# Devsy Start existing.devsy
Host existing.devsy
    User existing
# Devsy End existing.devsy`,
			expected: 0,
		},
		{
			name: "preamble stays before generated host",
			config: `Include ~/.ssh/common.conf
IdentityFile ~/.ssh/id_ed25519

Host existing
    User alice`,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position, lines, err := findInsertPosition(tt.config)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, position)
			assert.Equal(t, strings.Split(tt.config, "\n"), lines)
		})
	}
}

const (
	testSSHUser    = "devsy"
	testSSHContext = "context"
)

func TestAddHostSectionSectionSyntaxVariants(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "lowercase host", config: "host *\n  User alice"},
		{name: "uppercase host", config: "HOST *\n  User alice"},
		{name: "mixed-case host", config: "HoSt *\n  User alice"},
		{name: "tab separator", config: "Host\t*\n  User alice"},
		{name: "equals separator", config: "Host=*\n  User alice"},
		{name: "match first", config: "Match exec \"true\"\n  User alice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := addHostSection(tt.config, testExecPath, addHostParams{
				host:      "newhost",
				user:      testSSHUser,
				context:   testSSHContext,
				workspace: "workspace",
			})
			assert.NoError(t, err)
			assert.True(t, strings.HasPrefix(result, "# Devsy Start newhost\nHost newhost"))
			assert.Contains(t, result, tt.config)
		})
	}
}

func TestAddHostSectionPreservesExistingManagedBlock(t *testing.T) {
	const existingBlock = `# Devsy Start existing.devsy
Host existing.devsy
    User existing
# Devsy End existing.devsy`

	result, err := addHostSection(
		"host *\n  User alice\n\n"+existingBlock,
		testExecPath,
		addHostParams{
			host:      "newhost",
			user:      testSSHUser,
			context:   testSSHContext,
			workspace: "workspace",
		},
	)

	assert.NoError(t, err)
	assert.Contains(t, result, existingBlock)
	newStart := strings.Index(result, MarkerStartPrefix+"newhost")
	existingStart := strings.Index(result, MarkerStartPrefix+"existing.devsy")
	assert.NotEqual(t, -1, newStart)
	assert.NotEqual(t, -1, existingStart)
	assert.Less(t, newStart, existingStart)
}

func TestConfigureSSHConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	const existingBlock = `# Devsy Start existing.devsy
Host existing.devsy
    User existing
# Devsy End existing.devsy`
	initial := `host *
    IdentityFile ~/.ssh/id_ed25519
    IdentityFile ~/.ssh/id_ed25519

` + existingBlock + `

Match exec "true"
    User conditional`
	assert.NoError(t, os.WriteFile(configPath, []byte(initial), 0o600))

	err := ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: configPath,
		Context:       testSSHContext,
		Workspace:     "new-workspace",
		User:          testSSHUser,
	})
	assert.NoError(t, err)

	// #nosec G304 -- configPath is created within the test temporary directory.
	resultBytes, err := os.ReadFile(
		configPath,
	)
	assert.NoError(t, err)
	result := string(resultBytes)
	newHost := "new-workspace" + config.SSHHostSuffix
	newBlockStart := strings.Index(result, MarkerStartPrefix+newHost)
	existingBlockStart := strings.Index(result, MarkerStartPrefix+"existing.devsy")
	wildcardStart := strings.Index(result, "host *")
	matchStart := strings.Index(result, "Match exec \"true\"")

	assert.Contains(t, result, MarkerStartPrefix+newHost)
	assert.Contains(t, result, MarkerEndPrefix+newHost)
	assert.Contains(t, result, "Host "+newHost)
	assert.Contains(
		t,
		result,
		"host *\n    IdentityFile ~/.ssh/id_ed25519\n    IdentityFile ~/.ssh/id_ed25519",
	)
	assert.Contains(t, result, "Match exec \"true\"\n    User conditional")
	assert.Equal(t, 1, strings.Count(result, existingBlock))
	assert.Less(t, newBlockStart, wildcardStart)
	assert.Less(t, newBlockStart, existingBlockStart)
	assert.Less(t, newBlockStart, matchStart)
}

package dotfiles

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	fooBar                     = "FOO=bar"
	foo                        = "FOO"
	httpsGithubComUserDotfiles = "https://github.com/user/dotfiles"
	repository                 = "--repository"
)

func TestExtractKeysFromEnvKeyValuePairs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "single key-value pair",
			input:    []string{fooBar},
			expected: []string{foo},
		},
		{
			name:     "multiple key-value pairs",
			input:    []string{fooBar, "BAZ=qux"},
			expected: []string{foo, "BAZ"},
		},
		{
			name:     "value contains equals sign",
			input:    []string{"FOO=bar=baz"},
			expected: []string{foo},
		},
		{
			name:     "entry without equals sign is skipped",
			input:    []string{"NOEQ", fooBar},
			expected: []string{foo},
		},
		{
			name:     "empty value",
			input:    []string{"FOO="},
			expected: []string{foo},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractKeysFromEnvKeyValuePairs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCollectDotfilesScriptEnvKeyValuePairs(t *testing.T) {
	t.Run("empty file list", func(t *testing.T) {
		result, err := collectDotfilesScriptEnvKeyValuePairs([]string{})
		assert.NoError(t, err)
		assert.Equal(t, []string{}, result)
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		_, err := collectDotfilesScriptEnvKeyValuePairs([]string{"/nonexistent/file"})
		assert.Error(t, err)
	})
}

func TestBuildDotCmdAgentArguments(t *testing.T) {
	tests := []struct {
		name           string
		dotfilesRepo   string
		dotfilesScript string
		strictHostKey  bool
		debug          bool
		expected       []string
	}{
		{
			name:         "basic repo only",
			dotfilesRepo: httpsGithubComUserDotfiles,
			expected: []string{
				cmdInternal, cmdAgent, cmdWorkspace, cmdInstallDotfs,
				repository, httpsGithubComUserDotfiles,
			},
		},
		{
			name:           "with script",
			dotfilesRepo:   httpsGithubComUserDotfiles,
			dotfilesScript: "install.sh",
			expected: []string{
				cmdInternal, cmdAgent, cmdWorkspace, cmdInstallDotfs,
				repository, httpsGithubComUserDotfiles,
				"--install-script", "install.sh",
			},
		},
		{
			name:           "all options enabled",
			dotfilesRepo:   httpsGithubComUserDotfiles,
			dotfilesScript: "setup.sh",
			strictHostKey:  true,
			debug:          true,
			expected: []string{
				cmdInternal, cmdAgent, cmdWorkspace, cmdInstallDotfs,
				repository, httpsGithubComUserDotfiles,
				"--strict-host-key-checking", "--debug",
				"--install-script", "setup.sh",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildDotCmdAgentArguments(
				tt.dotfilesRepo,
				tt.dotfilesScript,
				tt.strictHostKey,
				tt.debug,
			)
			assert.Equal(t, tt.expected, result)
		})
	}
}

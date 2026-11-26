package docker

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	testDockerCmd      = "docker"
	dockerHostKey      = "DOCKER_HOST"
	dockerContextKey   = "DOCKER_CONTEXT"
	lowerDockerHostKey = "docker_host"
	processHostSock    = "unix:///process.sock"
)

type InvocationSuite struct {
	suite.Suite
}

func TestInvocationSuite(t *testing.T) {
	suite.Run(t, new(InvocationSuite))
}

func setCaseInsensitive(t *testing.T, v bool) {
	t.Helper()
	prev := envCaseInsensitive
	envCaseInsensitive = v
	t.Cleanup(func() { envCaseInsensitive = prev })
}

func newTestInvocation(
	s *InvocationSuite,
	processEnv map[string]string,
	provider ...string,
) *DockerInvocation {
	for k, v := range processEnv {
		s.T().Setenv(k, v)
	}
	inv, err := NewDockerInvocation(
		&DockerHelper{DockerCommand: testDockerCmd, Environment: provider},
	)
	s.Require().NoError(err)
	return inv
}

// countKeys counts environ entries whose key matches name, ignoring case.
func countKeys(environ []string, name string) int {
	n := 0
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, name) {
			n++
		}
	}
	return n
}

func (s *InvocationSuite) TestProviderEnvOverridesProcessEnv() {
	inv := newTestInvocation(
		s,
		map[string]string{
			"DEVSY_TEST_ONLY_PROCESS": "process",
			dockerHostKey:             processHostSock,
		},
		"DOCKER_HOST=ssh://provider",
		"EXTRA=a=b",
	)

	s.Equal(testDockerCmd, inv.DockerCmd)
	host, ok := inv.Lookup(dockerHostKey)
	s.True(ok)
	s.Equal("ssh://provider", host)
	process, ok := inv.Lookup("DEVSY_TEST_ONLY_PROCESS")
	s.True(ok)
	s.Equal("process", process)
	extra, _ := inv.Lookup("EXTRA")
	s.Equal("a=b", extra, "value keeps everything after the first '='")
	s.Equal(processHostSock, os.Getenv(dockerHostKey), "global process env must not change")
}

func (s *InvocationSuite) TestEmptyAndEqualsValuesAreExact() {
	inv := newTestInvocation(s, nil, "EMPTY=", "EQ=a=b=", "NOEQUALS", "=novalue")

	v, ok := inv.Lookup("EMPTY")
	s.True(ok, "explicit empty is distinct from absent")
	s.Empty(v)
	v, ok = inv.Lookup("EQ")
	s.True(ok)
	s.Equal("a=b=", v)
	_, ok = inv.Lookup("NOEQUALS")
	s.False(ok, "entries without '=' are not variables")
	_, ok = inv.Lookup("DEVSY_TEST_NEVER_SET")
	s.False(ok)
	s.Contains(inv.Environ(), "EMPTY=")
	s.Contains(inv.Environ(), "EQ=a=b=")
}

func (s *InvocationSuite) TestProviderExplicitEmptyOverridesProcessValue() {
	inv := newTestInvocation(
		s,
		map[string]string{dockerContextKey: "process-ctx"},
		"DOCKER_CONTEXT=",
	)

	v, ok := inv.Lookup(dockerContextKey)
	s.True(ok)
	s.Empty(v)
	s.Contains(inv.Environ(), "DOCKER_CONTEXT=")
	s.NotContains(inv.Environ(), "DOCKER_CONTEXT=process-ctx")
}

func (s *InvocationSuite) TestSnapshotIsIndependentOfLaterChanges() {
	s.T().Setenv("K", "process")
	helper := &DockerHelper{
		DockerCommand: testDockerCmd,
		Environment:   []string{"K=v1", "ONLY_PROVIDER=p"},
	}
	inv, err := NewDockerInvocation(helper)
	s.Require().NoError(err)

	helper.Environment[0] = "K=v2"
	helper.Environment = append(helper.Environment, "LATE=1")
	helper.DockerCommand = "other"
	s.T().Setenv("K", "other")
	s.T().Setenv("PROCESS_LATE", "1")
	s.Require().NoError(os.Unsetenv("ONLY_PROVIDER"))

	s.Equal(testDockerCmd, inv.DockerCmd)
	v, _ := inv.Lookup("K")
	s.Equal("v1", v)
	_, ok := inv.Lookup("LATE")
	s.False(ok)
	_, ok = inv.Lookup("PROCESS_LATE")
	s.False(ok)
	p, ok := inv.Lookup("ONLY_PROVIDER")
	s.True(ok)
	s.Equal("p", p)
}

func (s *InvocationSuite) TestReturnedSlicesAreCopies() {
	inv := newTestInvocation(s, nil, "A=1", "B=2")

	first := inv.Environ()
	want := slices.Clone(first)
	for idx := range first {
		first[idx] = "MUTATED=x"
	}
	_ = append(first, "EXTRA=y")

	s.Equal(want, inv.Environ())
	v, _ := inv.Lookup("A")
	s.Equal("1", v)
	_, ok := inv.Lookup("MUTATED")
	s.False(ok)
	s.NotSame(&first[0], &inv.Environ()[0])
}

func (s *InvocationSuite) TestWindowsProviderEnvReplacesDifferentlyCasedProcessKey() {
	setCaseInsensitive(s.T(), true)
	inv := newTestInvocation(s, map[string]string{dockerHostKey: processHostSock},
		"docker_host=ssh://provider")

	s.Equal(1, countKeys(inv.Environ(), dockerHostKey), "no duplicate keys may reach the child")
	for _, name := range []string{"DOCKER_HOST", lowerDockerHostKey, "Docker_Host"} {
		v, ok := inv.Lookup(name)
		s.True(ok, name)
		s.Equal("ssh://provider", v, name)
	}
}

func (s *InvocationSuite) TestWindowsLowercaseProcessKeyReplacedByProvider() {
	setCaseInsensitive(s.T(), true)
	inv := newTestInvocation(s, map[string]string{lowerDockerHostKey: processHostSock},
		"DOCKER_HOST=ssh://provider")

	s.Equal(1, countKeys(inv.Environ(), dockerHostKey))
	v, _ := inv.Lookup(lowerDockerHostKey)
	s.Equal("ssh://provider", v)
}

func (s *InvocationSuite) TestLinuxKeepsCaseSensitiveKeys() {
	setCaseInsensitive(s.T(), false)
	inv := newTestInvocation(s, map[string]string{lowerDockerHostKey: "unix:///lower.sock"},
		"DOCKER_HOST=ssh://provider")

	s.Equal(2, countKeys(inv.Environ(), dockerHostKey), "differently cased keys are distinct")
	lower, ok := inv.Lookup(lowerDockerHostKey)
	s.True(ok)
	s.Equal("unix:///lower.sock", lower)
	upper, _ := inv.Lookup("DOCKER_HOST")
	s.Equal("ssh://provider", upper)
	_, ok = inv.Lookup("Docker_Host")
	s.False(ok)
}

func (s *InvocationSuite) TestSelectorVariablesComeFromProvider() {
	process := map[string]string{}
	var provider []string
	for _, key := range daemonSelectorKeys {
		process[key] = "process-" + key
		provider = append(provider, key+"=provider-"+key)
	}
	inv := newTestInvocation(s, process, provider...)

	for _, key := range daemonSelectorKeys {
		v, ok := inv.Lookup(key)
		s.True(ok, key)
		s.Equal("provider-"+key, v, key)
		s.Contains(inv.Environ(), key+"=provider-"+key)
		s.Equal(1, countKeys(inv.Environ(), key), key)
	}
}

func (s *InvocationSuite) TestProviderContextInConfigReachesCLIAndSDKAlike() {
	s.T().Setenv(dockerHostKey, "")
	inv := newTestInvocation(s, nil, "DOCKER_CONFIG=/provider/cfg", "DOCKER_CONTEXT=wsl")

	// the CLI child (Environ) and the SDK-side lookups read one snapshot
	for _, key := range []string{"DOCKER_CONFIG", dockerContextKey} {
		v, ok := inv.Lookup(key)
		s.Require().True(ok, key)
		s.Contains(inv.Environ(), key+"="+v)
	}
	host, ok := inv.Lookup(dockerHostKey)
	s.True(ok)
	s.Empty(host, "no literal host is supplied; the context selects the daemon")
}

func (s *InvocationSuite) TestLookupCaseSensitivity() {
	inv := &DockerInvocation{DockerCmd: testDockerCmd, env: map[string]string{"Path": "x"}}

	setCaseInsensitive(s.T(), true)
	v, ok := inv.Lookup("PATH")
	s.True(ok)
	s.Equal("x", v)

	setCaseInsensitive(s.T(), false)
	_, ok = inv.Lookup("PATH")
	s.False(ok)
}

func (s *InvocationSuite) TestRejectsUnusableHelpers() {
	_, err := NewDockerInvocation(nil)
	s.Error(err)
	_, err = NewDockerInvocation(&DockerHelper{})
	s.Error(err)
	_, err = NewDockerInvocation(&DockerHelper{DockerCommand: testDockerCmd, Elevator: &Elevator{}})
	s.Error(err)
}

func (s *InvocationSuite) TestEnvironIsSortedKeyValuePairs() {
	inv := &DockerInvocation{DockerCmd: testDockerCmd, env: map[string]string{"B": "2", "A": "1"}}
	s.Equal([]string{"A=1", "B=2"}, inv.Environ())
}

// The invocation is deliberately not serializable: encoding it must never
// carry the environment, which can hold credentials.
func (s *InvocationSuite) TestJSONDoesNotExposeEnvironment() {
	inv := &DockerInvocation{
		DockerCmd: testDockerCmd,
		env:       map[string]string{"DOCKER_HOST": "ssh://fake-secret"},
	}
	data, err := json.Marshal(inv)
	s.Require().NoError(err)

	s.NotContains(string(data), "fake-secret")
	s.NotContains(string(data), "DOCKER_HOST")
}

func TestProviderDaemonOverrides(t *testing.T) {
	t.Setenv(dockerHostKey, "unix:///a.sock")
	t.Setenv(dockerContextKey, "")
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_CERT_PATH", "/certs")
	t.Setenv("DOCKER_CONFIG", "/cfg")

	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{"no overrides", nil, nil},
		{"identical values", []string{"DOCKER_HOST=unix:///a.sock", "DOCKER_CONFIG=/cfg"}, nil},
		{"empty equals absent", []string{"DOCKER_CONTEXT="}, nil},
		{"host differs", []string{"DOCKER_HOST=ssh://u@h"}, []string{dockerHostKey}},
		{
			"explicit empty clears set value",
			[]string{"DOCKER_TLS_VERIFY="},
			[]string{"DOCKER_TLS_VERIFY"},
		},
		{
			"several, stable order",
			[]string{"DOCKER_CERT_PATH=/other", "DOCKER_CONTEXT=x", "DOCKER_HOST=tcp://h"},
			[]string{dockerHostKey, dockerContextKey, "DOCKER_CERT_PATH"},
		},
		{"non-selector ignored", []string{"HTTP_PROXY=http://p"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProviderDaemonOverrides(&DockerHelper{Environment: tt.env})
			require.Equal(t, tt.want, got)
		})
	}
}

func TestProviderDaemonOverridesNilHelper(t *testing.T) {
	require.Nil(t, ProviderDaemonOverrides(nil))
}

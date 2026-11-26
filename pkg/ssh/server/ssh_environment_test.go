package server

import (
	"strings"
	"testing"

	"github.com/devsy-org/ssh"
	"github.com/stretchr/testify/require"
)

const sessionPrecedenceName = "DEVSY_SESSION_PRECEDENCE_SENTINEL"

type sessionEnvironmentStub struct {
	ssh.Session
	user        string
	environment []string
}

func (s sessionEnvironmentStub) User() string { return s.user }

func (s sessionEnvironmentStub) Environ() []string { return s.environment }

func (sessionEnvironmentStub) RawCommand() string { return "" }

func TestGetCommandPreservesSessionEnvironmentPrecedence(t *testing.T) {
	t.Setenv(sessionPrecedenceName, "base-sentinel")
	s := &server{currentUser: "workspace-user", shell: []string{"/bin/sh"}}
	sess := sessionEnvironmentStub{
		user:        "workspace-user",
		environment: []string{sessionPrecedenceName + "=explicit-sentinel"},
	}

	cmd, err := s.getCommandWithSecretEnvironment(
		sess,
		false,
		[]string{sessionPrecedenceName + "=attached-sentinel"},
	)
	require.NoError(t, err)

	got := ""
	for _, assignment := range cmd.Env {
		name, value, ok := strings.Cut(assignment, "=")
		if ok && name == sessionPrecedenceName {
			got = value
		}
	}
	require.Equal(t, "explicit-sentinel", got)
}

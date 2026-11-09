package sshtunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"testing"
	"time"

	sshserver "github.com/devsy-org/ssh"
	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/ssh"
)

type sessionEnvironmentSuite struct{ suite.Suite }

func TestSessionEnvironment(t *testing.T) { suite.Run(t, new(sessionEnvironmentSuite)) }

func (s *sessionEnvironmentSuite) TestCommandEnvironment() {
	for _, tc := range []struct {
		name        string
		environment map[string]string
		closed      bool
	}{
		{name: "default"},
		{name: "configured", environment: map[string]string{"MSB_PATH": "configured runtime"}},
		{
			name: "closed", environment: map[string]string{"MSB_PATH": "configured runtime"},
			closed: true,
		},
	} {
		s.Run(tc.name, func() {
			client, observed := s.newClient()
			session, err := client.NewSession()
			s.Require().NoError(err)
			defer func() { _ = session.Close() }()
			if tc.closed {
				s.Require().NoError(client.Close())
			}
			stdin, input, err := os.Pipe()
			s.Require().NoError(err)
			s.Require().NoError(input.Close())
			defer func() { _ = stdin.Close() }()
			output, stdout, err := os.Pipe()
			s.Require().NoError(err)
			defer func() { _ = output.Close() }()
			defer func() { _ = stdout.Close() }()
			err = runCommandInSSHTunnel(s.T().Context(), sshCommandParams{
				session: session, command: "probe", envVars: tc.environment,
			}, stdin, stdout)
			if tc.closed {
				s.Require().ErrorContains(err, "set provider environment")
				s.NotContains(err.Error(), "configured runtime")
				return
			}
			s.Require().NoError(err)
			if tc.environment == nil {
				s.Empty(<-observed)
				return
			}
			s.Equal([]string{"MSB_PATH=configured runtime"}, <-observed)
		})
	}
}

func (s *sessionEnvironmentSuite) newClient() (*ssh.Client, <-chan []string) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	s.Require().NoError(err)
	signer, err := ssh.NewSignerFromKey(key)
	s.Require().NoError(err)
	observed := make(chan []string, 1)
	server := &sshserver.Server{Handler: func(session sshserver.Session) {
		observed <- session.Environ()
	}}
	server.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	s.T().Cleanup(func() {
		s.NoError(server.Close())
		s.ErrorIs(<-done, sshserver.ErrServerClosed)
	})
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		Timeout:         5 * time.Second,
	})
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = client.Close() })
	return client, observed
}

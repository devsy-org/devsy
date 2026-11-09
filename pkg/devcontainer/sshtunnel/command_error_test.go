package sshtunnel

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

type commandErrorSuite struct{ suite.Suite }

func TestCommandErrorSuite(t *testing.T) { suite.Run(t, new(commandErrorSuite)) }

func (s *commandErrorSuite) TestCommandFailure() {
	for _, tc := range []struct {
		name       string
		output     string
		wrapsError bool
	}{
		{name: "agent diagnostic", output: "provider binary checksum mismatch"},
		{name: "no diagnostic", wrapsError: true},
	} {
		s.Run(tc.name, func() {
			exit := errors.New("exit status 1")
			err := commandFailure(exit, tc.output)
			s.ErrorContains(err, "exit status 1")
			if tc.wrapsError {
				s.ErrorIs(err, exit)
			} else {
				s.ErrorContains(err, tc.output)
				s.ErrorIs(err, exit)
			}
		})
	}
}

func (s *commandErrorSuite) TestCommandFailurePreservesCancellation() {
	err := commandFailure(context.Canceled, "agent diagnostic")
	s.ErrorIs(err, context.Canceled)
	s.ErrorContains(err, "agent diagnostic")
}

func (s *commandErrorSuite) TestStoredSecretIsRedactedFromAgentLogs() {
	const secret = "stored-secret-not-in-environment-5791"
	logs := log.InitTestObserved(s.T(), zapcore.DebugLevel)
	opts := ExecuteCommandOptions{
		Redactor: secrets.NewRedactor([]string{"VALUE=" + secret}),
		AgentInject: func(_ context.Context, _ string, _ io.Reader, _ io.Writer, stderr io.WriteCloser) error {
			_, err := io.WriteString(stderr, "agent: "+secret+"\n")
			return err
		},
	}
	s.Require().NoError(executeSSHServerHelper(context.Background(), opts, nil, io.Discard))
	for _, entry := range logs.All() {
		s.NotContains(entry.Message, secret)
	}
}

func (s *commandErrorSuite) TestStoredSecretIsRedactedFromCapturedDiagnostics() {
	for _, tc := range []struct{ name, value, output string }{
		{name: "plain", value: "stored-private-value-8231", output: "stored-private-value-8231"},
		{name: "json escaped", value: "stored-quote-\"-value-8231", output: `stored-quote-\"-value-8231`},
		{name: "multiline", value: "stored-first-8231\nstored-second-8231", output: "stored-first-8231\nstored-second-8231"},
	} {
		s.Run(tc.name, func() {
			streamer := newSSHTunnelJSONLogStreamer(
				secrets.NewRedactor([]string{"VALUE=" + tc.value}),
			)
			_, err := io.WriteString(streamer, tc.output+"\n")
			s.Require().NoError(err)
			s.Require().NoError(streamer.Close())
			s.NotContains(streamer.ErrorOutput(), "stored-")
		})
	}
}

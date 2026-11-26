package sshtunnel

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type streamerSafetySuite struct{ suite.Suite }

func TestStreamerSafetySuite(t *testing.T) {
	suite.Run(t, new(streamerSafetySuite))
}

func (s *streamerSafetySuite) TestAgentOutputIsNotRetainedForErrors() {
	streamer := newSSHTunnelJSONLogStreamer()
	_, err := streamer.Write([]byte("synthetic-workspace-credential\n"))
	s.Require().NoError(err)
	s.Require().NoError(streamer.Close())
	s.Empty(streamer.ErrorOutput())
}

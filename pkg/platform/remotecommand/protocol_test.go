package remotecommand

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/suite"
)

type protocolSuite struct {
	suite.Suite
}

func TestProtocolSuite(t *testing.T) {
	suite.Run(t, new(protocolSuite))
}

func (s *protocolSuite) TestDataMessageRoundTrip() {
	original := newDataMessage(StdoutData, []byte("hello"))
	encoded := original.Bytes()
	s.Equal([]byte{byte(StdoutData), 'h', 'e', 'l', 'l', 'o'}, encoded)

	parsed, err := ParseMessage(bytes.NewReader(encoded))
	s.Require().NoError(err)
	s.Equal(StdoutData, parsed.messageType)
	payload, err := io.ReadAll(parsed.data)
	s.Require().NoError(err)
	s.Equal("hello", string(payload))
}

func (s *protocolSuite) TestCloseMessagesCarryNoPayload() {
	for _, messageType := range []MessageType{StdoutClose, StderrClose, StdinClose} {
		encoded := newCloseMessage(messageType).Bytes()
		s.Equal([]byte{byte(messageType)}, encoded)

		parsed, err := ParseMessage(bytes.NewReader(encoded))
		s.Require().NoError(err, "type %d", messageType)
		s.Equal(messageType, parsed.messageType, "type %d", messageType)
		s.Nil(parsed.data, "type %d", messageType)
	}
}

func (s *protocolSuite) TestExitCodeRoundTrip() {
	original := NewExitCodeMessage(42)
	encoded := original.Bytes()
	s.Equal(byte(ExitCode), encoded[0])

	parsed, err := ParseMessage(bytes.NewReader(encoded))
	s.Require().NoError(err)
	s.Equal(ExitCode, parsed.messageType)
	s.Equal(int64(42), parsed.exitCode)
}

func (s *protocolSuite) TestExitCodeRoundTripNegative() {
	encoded := NewExitCodeMessage(-1).Bytes()
	parsed, err := ParseMessage(bytes.NewReader(encoded))
	s.Require().NoError(err)
	s.Equal(ExitCode, parsed.messageType)
	s.Equal(int64(-1), parsed.exitCode)
}

func (s *protocolSuite) TestParseRejectsUnknownMessageType() {
	_, err := ParseMessage(bytes.NewReader([]byte{99}))
	s.Require().Error(err)
	// Rendered in binary, matching the production error.
	s.Contains(err.Error(), "1100011")
}

func (s *protocolSuite) TestParseRejectsTruncatedExitCode() {
	// A type byte with no varint payload behind it.
	_, err := ParseMessage(bytes.NewReader([]byte{byte(ExitCode)}))
	s.Require().Error(err)
	s.Contains(err.Error(), "read exit code")
}

func (s *protocolSuite) TestParseReturnsErrorOnEmptyInput() {
	_, err := ParseMessage(bytes.NewReader(nil))
	s.ErrorIs(err, io.EOF)
}

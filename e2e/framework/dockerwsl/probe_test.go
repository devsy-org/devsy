package dockerwsl

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

type DockerProbeSuite struct {
	suite.Suite
	deps   *runenv.Dependencies
	dialer *scriptedDialer
}

func TestDockerProbeSuite(t *testing.T) { suite.Run(t, new(DockerProbeSuite)) }

func (s *DockerProbeSuite) SetupTest() {
	s.dialer = &scriptedDialer{answer: map[string]string{}}
	s.deps = &runenv.Dependencies{Net: s.dialer, Clock: runenvtest.FakeClock{}}
}

func (s *DockerProbeSuite) Probe(addr string) error {
	return probeSSHBanner(context.Background(), s.dialer, addr, 500*time.Millisecond)
}

func (s *DockerProbeSuite) TestBannerSuccessSurvivesPartialRead() {
	// scriptedDialer writes the first two bytes separately from the rest.
	s.dialer.answer["h:22"] = testBanner
	s.Require().NoError(s.Probe("h:22"))
}

func (s *DockerProbeSuite) TestNonSSHPeerIsRejected() {
	s.dialer.answer["h:22"] = httpBanner
	err := s.Probe("h:22")
	s.Require().Error(err)
	s.Contains(err.Error(), "not an SSH identification line")
}

func (s *DockerProbeSuite) TestAcceptThenCloseIsNotABanner() {
	// A relay that accepts TCP and closes proves nothing.
	s.dialer.answer["h:22"] = ""
	err := s.Probe("h:22")
	s.Require().Error(err)
	s.Contains(err.Error(), "read banner")
}

func (s *DockerProbeSuite) TestSilentPeerTimesOut() {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	err := readWithDeadline(client, 50*time.Millisecond)
	s.Require().Error(err)
}

func readWithDeadline(conn net.Conn, d time.Duration) error {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	return readSSHBanner(conn, "pipe")
}

func (s *DockerProbeSuite) TestDialFailure() {
	err := s.Probe("nowhere:22")
	s.Require().Error(err)
	s.Contains(err.Error(), "dial nowhere:22")
}

func (s *DockerProbeSuite) TestSelectsLoopbackFirst() {
	s.dialer.answer["127.0.0.1:2222"] = testBanner
	s.dialer.answer[testVMIP+":2222"] = testBanner

	host, err := selectSSHAddress(context.Background(), s.deps, sshCandidates([]string{testVMIP}))

	s.Require().NoError(err)
	s.Equal("127.0.0.1", host)
	s.Equal([]string{"127.0.0.1:2222"}, s.dialer.dialed[:1])
}

func (s *DockerProbeSuite) TestFallsBackToVMAddress() {
	s.dialer.answer["127.0.0.1:2222"] = httpBanner
	s.dialer.answer[testVMIP+":2222"] = testBanner

	host, err := selectSSHAddress(context.Background(), s.deps, sshCandidates([]string{testVMIP}))

	s.Require().NoError(err)
	s.Equal(testVMIP, host)
	s.GreaterOrEqual(len(s.dialer.dialed), loopbackAttempts, "loopback exhausted its retries first")
}

func (s *DockerProbeSuite) TestNoCandidateReportsEveryAttempt() {
	_, err := selectSSHAddress(context.Background(), s.deps, sshCandidates([]string{testVMIP}))
	s.Require().Error(err)
	s.Contains(err.Error(), "127.0.0.1:2222")
	s.Contains(err.Error(), testVMIP+":2222")
}

func (s *DockerProbeSuite) TestCancelledContextStopsSelection() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := selectSSHAddress(ctx, s.deps, sshCandidates([]string{testVMIP}))
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *DockerProbeSuite) TestParseVMAddresses() {
	s.Equal(
		[]string{"172.20.1.5", "172.17.0.1"},
		parseVMAddresses(
			"172.20.1.5 127.0.0.1 169.254.1.1 fe80::1 0.0.0.0 172.17.0.1 172.20.1.5 junk\n",
		),
	)
	s.Empty(parseVMAddresses(""))
}

func (s *DockerProbeSuite) TestParseDaemonID() {
	id, err := parseDaemonID(" " + testDaemonID + "\r\n")
	s.Require().NoError(err)
	s.Equal(testDaemonID, id)
	for _, bad := range []string{"", "  \n", "short", "two words here", "a\nb\nc\nd\ne\nf\ng\nh"} {
		_, err := parseDaemonID(bad)
		s.Error(err, bad)
	}
}

func (s *DockerProbeSuite) TestCompareDaemonIDs() {
	s.Require().NoError(compareDaemonIDs("A", "A"))
	s.Error(compareDaemonIDs("A", "B"))
	s.Error(compareDaemonIDs("", ""))
	s.Error(compareDaemonIDs("A", ""))
}

func (s *DockerProbeSuite) TestDeadPipeOracle() {
	s.Require().NoError(requireDeadPipeFailure(runenv.CommandResult{ExitCode: 1}, nil))
	s.Require().
		Error(requireDeadPipeFailure(runenv.CommandResult{}, nil), "success invalidates the oracle")
	s.Require().
		Error(requireDeadPipeFailure(runenv.CommandResult{TimedOut: true, ExitCode: -1}, nil))
	s.Require().
		Error(requireDeadPipeFailure(runenv.CommandResult{ExitCode: -1}, errors.New("start failed")))
}

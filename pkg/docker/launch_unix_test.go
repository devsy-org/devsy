//go:build !windows

package docker

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
)

func (s *StdioTransportSuite) TestCloseKillsDescendantAfterParentExits() {
	transport, rec := recordedTransport(s, "spawn-child-exit")
	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)
	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(boundedInterval)))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	s.Require().NoError(err)
	descendant, err := strconv.Atoi(strings.TrimSpace(line))
	s.Require().NoError(err)
	identity, err := command.ProcessTreeIdentity(descendant)
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		if processRunning(descendant) {
			s.NoError(
				command.KillTreeWithIdentity(
					strconv.Itoa(descendant),
					"stdio-test-child",
					identity,
				),
			)
		}
	})

	_, err = io.Copy(io.Discard, reader)
	s.Require().NoError(err)
	s.Require().NoError(conn.Close())
	s.Require().NoError(transport.Close())
	rec.requireAllReaped(s, 1)
	s.Eventually(func() bool { return !processRunning(descendant) }, boundedInterval,
		10*time.Millisecond, "descendant survived normal parent exit and connection Close")
}

func (s *StdioTransportSuite) TestFastExitsRetainProcessIdentity() {
	for range 10 {
		launched, err := launchDialStdio(launchSpec{path: "true"})
		s.Require().NoError(err)
		closeAll(launched.stdin, launched.stdout, launched.stderr)
		s.Require().NoError(launched.proc.Wait())
		s.Require().NoError(launched.proc.Release())
	}
}

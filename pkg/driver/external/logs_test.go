package external

import (
	"bytes"
	"context"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *HostSuite) TestLogsMergedBinaryOutput() {
	host := s.runningHost(fake.Normal)
	var stdout, stderr bytes.Buffer
	s.Require().
		NoError(host.GetDevContainerLogs(context.Background(), fixtureWorkspace, &stdout, &stderr))
	s.Equal([]byte("fake stdout\x00\xff\nfake stderr\n"), stdout.Bytes())
	s.Empty(stderr.Bytes())
	s.NoError(host.GetDevContainerLogs(context.Background(), fixtureWorkspace, nil, nil))
}

func (s *HostSuite) TestLogsCapabilityAndValidationBeforeLaunch() {
	host := s.host(fake.Normal)
	host.info.Capabilities.Logs = false
	host.config.Binaries[fixtureKey][0].Checksum = "bad"
	err := host.GetDevContainerLogs(context.Background(), fixtureWorkspace, nil, nil)
	s.Equal(codes.Unimplemented, status.Code(err))
	s.ErrorContains(host.GetDevContainerLogs(context.Background(), "", nil, nil), "workspace ID")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ErrorIs(host.GetDevContainerLogs(ctx, fixtureWorkspace, nil, nil), context.Canceled)
}

func (s *HostSuite) TestLogsWriterAndProtocolFailures() {
	host := s.runningHost(fake.Normal)
	s.ErrorContains(
		host.GetDevContainerLogs(context.Background(), fixtureWorkspace, shortWriter{}, nil),
		"short write",
	)
	for _, mode := range []string{"stream-logs-empty", "stream-logs-large"} {
		host := s.host(mode)
		s.ErrorContains(host.GetDevContainerLogs(context.Background(), fixtureWorkspace, nil, nil),
			"1..32768 bytes")
	}
}

func (s *HostSuite) TestLogsCancellationReapsProcess() {
	host := s.host("stream-logs-block")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.GetDevContainerLogs(ctx, fixtureWorkspace, nil, nil) }()
	s.waitForStream(host)
	cancel()
	select {
	case err := <-done:
		s.ErrorIs(err, context.Canceled)
	case <-time.After(5 * time.Second):
		s.FailNow("Logs cancellation did not return")
	}
}

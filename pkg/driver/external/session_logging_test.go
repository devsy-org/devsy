package external

import (
	"context"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy/pkg/log"
	"go.uber.org/zap/zapcore"
)

func (s *HostSuite) TestDebugKeepsRuntimeDiagnosticsWithoutHelperShutdownNoise() {
	s.T().Setenv("FIXTURE_TOKEN", "private-startup-canary")
	observed := log.InitTestObserved(s.T(), zapcore.DebugLevel)
	host := s.host("stream-startup-stderr")
	s.NotEmpty(host.Info().RuntimeName)

	s.Positive(observed.FilterMessageSnippet("runtime startup diagnostic ***").Len())
	s.Positive(observed.FilterMessageSnippet("External runtime Info startup completed").Len())
	s.Zero(observed.FilterMessageSnippet("received EOF, stopping recv loop").Len())
	s.Zero(observed.FilterMessageSnippet("plugin process exited").Len())
	for _, entry := range observed.All() {
		s.NotContains(entry.Message, "private-startup-canary")
	}
}

func (s *HostSuite) TestTraceKeepsHelperLifecycleDiagnostics() {
	observed := log.InitTestObserved(s.T(), zapcore.DebugLevel-1)
	host := s.host(fake.Normal)
	s.NotEmpty(host.Info().RuntimeName)

	s.Positive(observed.FilterMessageSnippet("plugin process exited").Len())
	// go-plugin joins process cleanup, but its gRPC stdio receive loop exits asynchronously.
	s.Require().Eventually(func() bool {
		return observed.FilterMessageSnippet("received EOF, stopping recv loop").Len() > 0
	}, 5*time.Second, 10*time.Millisecond)
}

func (s *HostSuite) TestDebugKeepsUnexpectedHelperExitDiagnostics() {
	observed := log.InitTestObserved(s.T(), zapcore.DebugLevel)
	_, err := s.newHost(context.Background(), fake.CrashBeforeHandshake, startupTimeout)
	s.Require().Error(err)
	s.Positive(observed.FilterMessageSnippet("plugin process exited").Len())
	s.Positive(observed.FilterMessageSnippet("exit status 22").Len())
}

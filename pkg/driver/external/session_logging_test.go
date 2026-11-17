package external

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
)

func (s *HostSuite) TestDebugKeepsRuntimeDiagnosticsWithoutHelperShutdownNoise() {
	s.T().Setenv("FIXTURE_TOKEN", "private-startup-canary")
	observed := log.InitTestObserved(s.T(), log.SlogLevelDebug)
	host := s.host("stream-startup-stderr")
	s.NotEmpty(host.Info().RuntimeName)

	s.Positive(observed.FilterMessageSnippet("runtime startup diagnostic ***").Len())
	s.Positive(observed.FilterMessageSnippet("External runtime startup completed").Len())
	s.Zero(observed.FilterMessageSnippet("received EOF, stopping recv loop").Len())
	s.Zero(observed.FilterMessageSnippet("plugin process exited").Len())
	for _, entry := range observed.All() {
		s.NotContains(entry.Message, "private-startup-canary")
	}
}

func (s *HostSuite) TestTraceKeepsHelperLifecycleDiagnostics() {
	observed := log.InitTestObserved(s.T(), log.SlogLevelTrace)
	host := s.host(fake.Normal)
	s.NotEmpty(host.Info().RuntimeName)

	s.Positive(observed.FilterMessageSnippet("plugin process exited").Len())
	// go-plugin joins process cleanup, but its gRPC stdio receive loop exits asynchronously.
	s.Require().Eventually(func() bool {
		return observed.FilterMessageSnippet("received EOF, stopping recv loop").Len() > 0
	}, 5*time.Second, 10*time.Millisecond)
}

func (s *HostSuite) TestDebugKeepsUnexpectedHelperExitDiagnostics() {
	observed := log.InitTestObserved(s.T(), log.SlogLevelDebug)
	_, err := s.newHost(context.Background(), fake.CrashBeforeHandshake, startupTimeout)
	s.Require().Error(err)
	s.Positive(observed.FilterMessageSnippet("plugin process exited").Len())
	s.Positive(observed.FilterMessageSnippet("exit status 22").Len())
}

func (s *HostSuite) TestRuntimeOperationMetricsPreserveClassificationWithoutDiagnostics() {
	host := s.host("env-error")
	metrics := telemetry.NewMetricsReporter()
	ctx := telemetry.WithMetrics(context.Background(), metrics)
	err := host.RunImage(ctx, &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace,
		Image:       fixtureImage,
		Environment: map[string]string{"CUSTOM_VALUE": workspaceCanary},
	})
	s.Require().Error(err)
	snapshot := metrics.Finish(err)
	s.Require().Len(snapshot.ProviderOperations, 1)
	s.Equal(telemetry.ProviderTypeExternalSDK, snapshot.ProviderOperations[0].ProviderType)
	s.Equal("RunImage", snapshot.ProviderOperations[0].Operation)
	s.Equal("unavailable", snapshot.ProviderOperations[0].RuntimeErrorCode)
	encoded, marshalErr := json.Marshal(snapshot)
	s.Require().NoError(marshalErr)
	s.NotContains(string(encoded), workspaceCanary)
}

func (s *HostSuite) TestRuntimeStructuredDiagnosticsKeepAttributesAndRedact() {
	s.T().Setenv("FIXTURE_TOKEN", "private-structured-canary")
	observed := log.InitTestObserved(s.T(), log.SlogLevelDebug)
	host := s.host("stream-structured-stderr")
	host.providerName = "provider-canary"
	observed.TakeAll()
	s.Require().NoError(host.Preflight(context.Background(), driver.PreflightOptions{}))
	entries := observed.FilterMessage("structured runtime canary").All()
	s.Require().NotEmpty(entries)
	s.Equal(log.SlogLevelWarn, entries[0].Level)
	attrs := entries[0].AttrsMap()
	s.Equal("external-driver", attrs["component"])
	s.Equal("provider-canary", attrs["provider"])
	data, err := json.Marshal(attrs)
	s.Require().NoError(err)
	s.Contains(string(data), "***")
	s.NotContains(string(data), "private-structured-canary")
}

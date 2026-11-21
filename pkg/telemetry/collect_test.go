package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/telemetry/analytics"
	"github.com/spf13/cobra"
)

type captureAnalytics struct{ events []analytics.Event }

func (c *captureAnalytics) RecordEvent(e analytics.Event) { c.events = append(c.events, e) }
func (c *captureAnalytics) Flush()                        {}

const operationRunImage = "RunImage"

func TestRecordCLIMetricsPayload(t *testing.T) {
	t.Setenv(config.EnvTelemetryDistinctID, "synthetic-override")
	t.Setenv(config.EnvWorkspaceUID, "private-workspace-uid")
	capture := &captureAnalytics{}
	collector := &cliCollector{analyticsClient: capture, cmd: &cobra.Command{Use: "up"}}
	snapshot := MetricsSnapshot{
		DurationMs: 9, PhaseDurationsMs: map[string]int64{phaseBuild: 5}, FailedPhase: phaseBuild,
		ErrorCode: "UNKNOWN",
		ProviderOperations: []ProviderMetric{{
			ProviderType: ProviderTypeExternalSDK, Operation: operationRunImage, Count: 1,
			DurationMs: 4, RuntimeErrorCode: "runtime_failure",
		}},
	}
	collector.RecordCLI(nil, snapshot)
	if len(capture.events) != 1 {
		t.Fatalf("events = %d", len(capture.events))
	}
	assertRecordedMetrics(t, capture.events[0]["event"])
}

func assertRecordedMetrics(t *testing.T, event map[string]any) {
	t.Helper()
	if event[analytics.KeyType] != "cli_command_executed" ||
		event["duration_ms"] != int64(9) || event["failed_phase"] != phaseBuild {
		t.Fatalf("unexpected payload: %v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-workspace-uid") ||
		!strings.Contains(
			string(encoded),
			"runtime_failure",
		) || !strings.Contains(string(encoded), operationRunImage) {
		t.Fatalf("unsafe or incomplete payload: %s", encoded)
	}
}

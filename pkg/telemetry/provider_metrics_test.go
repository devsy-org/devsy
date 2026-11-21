package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestProviderMetricsClassificationAndDuration(t *testing.T) {
	m := NewMetricsReporter()
	ctx := WithMetrics(context.Background(), m)
	for range 2 {
		RecordProviderOperation(ctx, ProviderOperation{
			ProviderType: ProviderTypeExternalSDK, Operation: "Find",
			Duration: 1500 * time.Microsecond, ErrorCode: "permission_denied",
		})
	}
	RecordProviderOperation(ctx, ProviderOperation{
		ProviderType: ProviderType(
			"private hostname",
		),
		Operation: "private command",
		ErrorCode: "private error",
	})
	snapshot := m.Finish(nil)
	assertProviderAggregates(t, snapshot)
}

func assertProviderAggregates(t *testing.T, snapshot MetricsSnapshot) {
	t.Helper()
	var found, unknown bool
	for _, metric := range snapshot.ProviderOperations {
		if isExpectedProviderMetric(metric) {
			found = true
		}
		if metric.ProviderType == ProviderTypeUnknown && metric.Operation == metricOther &&
			metric.RuntimeErrorCode == "unknown" {
			unknown = true
		}
	}
	if !found || !unknown {
		t.Fatalf("unexpected aggregates: %+v", snapshot.ProviderOperations)
	}
}

func isExpectedProviderMetric(metric ProviderMetric) bool {
	return metric.ProviderType == ProviderTypeExternalSDK && metric.Operation == "Find" &&
		metric.Count == 2 && metric.DurationMs == 3 && metric.RuntimeErrorCode == "permission_denied"
}

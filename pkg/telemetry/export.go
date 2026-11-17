package telemetry

import (
	"encoding/json"
	"errors"
	"os"
)

var errMetricsExport = errors.New("metrics export failed")

func ExportMetrics(snapshot MetricsSnapshot) error {
	path := os.Getenv("DEVSY_METRICS_FILE")
	if path == "" {
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > 65535 {
		return errMetricsExport
	}
	data = append(data, '\n')
	if err := appendMetricsFile(path, data); err != nil {
		return errMetricsExport
	}
	return nil
}

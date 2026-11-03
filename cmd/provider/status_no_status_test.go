package provider

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/require"
)

func TestStatusReporterNoStatus(t *testing.T) {
	for _, format := range []string{"plain", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			reporter, err := newStatusReporter(format, &out, false, true)
			require.NoError(t, err)
			reporter.Report(status.Event{Phase: status.PhaseReady, State: status.StateSucceeded})
			require.Empty(t, out.String())
		})
	}
}

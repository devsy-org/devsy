package cmdinternal

import (
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/require"
)

func TestLifecycleStatusReporterNoStatus(t *testing.T) {
	for _, format := range []string{"plain", "json"} {
		t.Run(format, func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "status")
			require.NoError(t, err)
			t.Cleanup(func() { _ = out.Close() })
			reporter, err := newLifecycleStatusReporter(format, out, false, true)
			require.NoError(t, err)
			reporter.Report(status.Event{Phase: status.PhaseReady, State: status.StateSucceeded})
			info, err := out.Stat()
			require.NoError(t, err)
			require.Zero(t, info.Size())
		})
	}
}

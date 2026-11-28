package telemetry

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestTelemetryOffFollowsTheAnnotationAndTheEnvironment(t *testing.T) {
	t.Setenv(config.EnvDisableTelemetry, "")
	plain := &cobra.Command{Use: "plain"}
	marked := &cobra.Command{Use: "marked", Annotations: NoTelemetryAnnotation()}

	assert.False(t, telemetryOff(plain))
	assert.False(t, telemetryOff(nil))
	assert.True(t, telemetryOff(marked))

	t.Setenv(config.EnvDisableTelemetry, config.BoolTrue)
	assert.True(t, telemetryOff(plain))
	assert.True(t, telemetryOff(nil))
}

package kubernetes

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
)

// The Kubernetes driver only execs into the existing pod, so exec-only calls
// cannot start anything.
func TestKubernetesDriverSupportsRequireRunning(t *testing.T) {
	assert.True(t, driver.DriverSupportsRequireRunning(&KubernetesDriver{}))
}

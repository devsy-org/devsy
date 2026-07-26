package apple

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
)

// TestDriverCapabilities locks in the capability design: the Apple driver
// supports image operations (ImageDriver) but deliberately does NOT advertise
// compose or the docker-helper capability, so callers gate on those via type
// assertion rather than a runtime error.
func TestDriverCapabilities(t *testing.T) {
	var d driver.Driver = &appleDriver{}

	if _, ok := d.(driver.ImageDriver); !ok {
		t.Error("apple driver must implement ImageDriver (image operations)")
	}
	if _, ok := d.(driver.ComposeDriver); ok {
		t.Error("apple driver must NOT implement ComposeDriver: container has no compose engine")
	}
	if _, ok := d.(driver.DockerHelperProvider); ok {
		t.Error("apple driver must NOT implement DockerHelperProvider: no docker helper")
	}
	// Image drivers run via RunImageDevContainer, not the RunOptionsDriver path
	// used by orchestrated drivers (Kubernetes/custom).
	if _, ok := d.(driver.RunOptionsDriver); ok {
		t.Error("apple driver must NOT implement RunOptionsDriver: it is an ImageDriver")
	}
}

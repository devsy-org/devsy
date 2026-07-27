package microsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
)

func TestPreflightInstalled(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	if err := d.Preflight(context.Background(), driver.PreflightOptions{}); err != nil {
		t.Fatalf("Preflight with runtime installed = %v, want nil", err)
	}
}

func TestPreflightNotInstalled(t *testing.T) {
	c := newFakeClient()
	c.failInstall = errors.New("msb not found")
	d := newDriver(c, nil, specDefaults{})

	err := d.Preflight(context.Background(), driver.PreflightOptions{})
	var perr *driver.PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *driver.PreflightError, got %v (%T)", err, err)
	}
	if perr.Provider != "microsandbox" {
		t.Fatalf("Provider = %q, want microsandbox", perr.Provider)
	}
}

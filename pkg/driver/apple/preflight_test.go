package apple

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
)

func TestApplePreflightBinaryMissing(t *testing.T) {
	d := &appleDriver{command: "devsy-nonexistent-container-binary", Apple: &mockClient{}}

	err := d.Preflight(context.Background(), driver.PreflightOptions{})
	var perr *driver.PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *driver.PreflightError, got %v (%T)", err, err)
	}
}

func TestApplePreflightAutoStart(t *testing.T) {
	m := &mockClient{systemDown: true}
	d := &appleDriver{command: "sh", Apple: m}

	if err := d.Preflight(context.Background(), driver.PreflightOptions{}); err != nil {
		t.Fatalf("Preflight with auto-start = %v, want nil", err)
	}
}

func TestApplePreflightOptOutSystemDown(t *testing.T) {
	m := &mockClient{systemDown: true}
	d := &appleDriver{command: "sh", Apple: m}

	err := d.Preflight(context.Background(), driver.PreflightOptions{DisableAutoStart: true})
	var perr *driver.PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *driver.PreflightError when opted out and system down, got %v", err)
	}
}

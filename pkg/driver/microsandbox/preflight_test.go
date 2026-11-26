package microsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
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

func TestPreflightAllowsOldRuntime(t *testing.T) {
	c := newFakeClient()
	c.version = oldVersion
	d := newDriver(c, nil, specDefaults{})

	if err := d.Preflight(context.Background(), driver.PreflightOptions{}); err != nil {
		t.Fatalf("Preflight with old runtime = %v, want nil", err)
	}
	if c.versionCalls != 0 {
		t.Fatalf("Preflight version probes = %d, want 0", c.versionCalls)
	}
}

func TestProvisioningPreflightAllowsOldRuntimeWhenStatVirtualizationOff(t *testing.T) {
	c := newFakeClient()
	c.version = oldVersion
	policy, err := parseWorkspaceMountPolicy(provider.ProviderMicrosandboxDriverConfig{
		WorkspaceHostPermissions:    string(hostPermissionsPrivate),
		WorkspaceStatVirtualization: string(statVirtOff),
	})
	if err != nil {
		t.Fatalf("parseWorkspaceMountPolicy: %v", err)
	}
	d := newDriver(c, nil, specDefaults{})
	d.workspaceMountPolicy = policy

	if err := d.ProvisioningPreflight(context.Background()); err != nil {
		t.Fatalf("ProvisioningPreflight with stat virtualization off = %v, want nil", err)
	}
	if c.versionCalls != 0 {
		t.Fatalf(
			"version probes = %d, want 0 when ownership synchronization is off",
			c.versionCalls,
		)
	}
}

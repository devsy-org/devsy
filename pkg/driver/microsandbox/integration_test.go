//go:build microsandbox_integration

// Integration test that drives the microsandbox driver against the real runtime.
// Requires the microsandbox runtime installed and network access to pull the
// image. Run with: go test -tags microsandbox_integration ./pkg/driver/microsandbox/
package microsandbox

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/driver"
)

func TestDriverLifecycleIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d := newDriver(cliClient{}, nil, specDefaults{memory: 2048, cpus: 2})
	const workspaceID = "integration-test"

	// Clean up any residue from a prior run, and always at the end.
	_ = d.DeleteDevContainer(ctx, workspaceID)
	defer func() { _ = d.DeleteDevContainer(ctx, workspaceID) }()

	if err := d.RunDevContainer(ctx, workspaceID, &driver.RunOptions{
		Image: "mcr.microsoft.com/devcontainers/base:ubuntu",
	}); err != nil {
		t.Fatalf("RunDevContainer: %v", err)
	}

	details, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		t.Fatalf("FindDevContainer: %v", err)
	}
	if details == nil {
		t.Fatal("FindDevContainer returned nil for a running workspace")
	}
	if details.State.Status != "running" {
		t.Errorf("status = %q, want running", details.State.Status)
	}

	var stdout, stderr bytes.Buffer
	if err := d.CommandDevContainer(ctx, &driver.CommandParams{
		WorkspaceID: workspaceID,
		Command:     "echo hello-from-vm && uname -r",
		Stdout:      &stdout,
		Stderr:      &stderr,
	}); err != nil {
		t.Fatalf("CommandDevContainer: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello-from-vm") {
		t.Errorf("command stdout = %q, want it to contain hello-from-vm", stdout.String())
	}
	t.Logf("command output:\n%s", stdout.String())
}

func TestLoadViaRegistryIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const img = "mcr.microsoft.com/devcontainers/base:ubuntu"
	if err := loadViaRegistry(ctx, img); err != nil {
		t.Fatalf("loadViaRegistry (go-containerregistry pull -> tarball -> msb load): %v", err)
	}
	// Boot the loaded image and confirm it runs.
	d := newDriver(cliClient{}, nil, specDefaults{})
	const workspaceID = "gcrload-test"
	defer func() { _ = d.DeleteDevContainer(ctx, workspaceID) }()
	if err := d.client.Create(ctx, sandboxName(workspaceID), sandboxSpec{Image: img}); err != nil {
		t.Fatalf("create from registry-loaded image: %v", err)
	}
	var out bytes.Buffer
	if err := d.CommandDevContainer(ctx, &driver.CommandParams{
		WorkspaceID: workspaceID, Command: "echo gcr-load-ok", Stdout: &out,
	}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(out.String(), "gcr-load-ok") {
		t.Errorf("output = %q", out.String())
	}
}

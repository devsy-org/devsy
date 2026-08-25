package apple

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// probe bundles the state shared across the integration steps so each helper
// stays within the project's argument limit.
type probe struct {
	t   *testing.T
	h   *AppleHelper
	ctx context.Context
}

// TestAppleHelperIntegration exercises AppleHelper against a real `container`
// CLI. It is gated behind DEVSY_APPLE_E2E=1 so it never runs in normal CI; it
// requires an Apple silicon Mac (macOS 26+) with `container` installed.
func TestAppleHelperIntegration(t *testing.T) {
	if os.Getenv("DEVSY_APPLE_E2E") != "1" {
		t.Skip("set DEVSY_APPLE_E2E=1 to run against a real container CLI")
	}

	const (
		image = "alpine:latest"
		name  = "devsy-e2e-probe"
		label = "devsy.e2e=1"
	)

	p := &probe{t: t, h: &AppleHelper{Command: "container"}, ctx: context.Background()}

	if err := p.h.EnsureSystemRunning(p.ctx); err != nil {
		t.Fatalf("EnsureSystemRunning: %v", err)
	}

	p.pullImage(image)
	_ = p.h.Remove(p.ctx, name) // best-effort cleanup of a stale run
	p.runProbe(name, label, image)
	t.Cleanup(func() {
		_ = p.h.Stop(context.Background(), name)
		_ = p.h.Remove(context.Background(), name)
	})

	found := p.findRunning(label)
	p.execEcho(found)
	p.teardown(found)
}

func (p *probe) pullImage(image string) {
	p.t.Helper()
	var out bytes.Buffer
	if err := p.h.Pull(p.ctx, PullOptions{Image: image, Stdout: &out, Stderr: &out}); err != nil {
		p.t.Fatalf("Pull: %v\n%s", err, out.String())
	}
	if _, err := p.h.InspectImage(p.ctx, image, false); err != nil {
		p.t.Fatalf("InspectImage: %v", err)
	}
}

func (p *probe) runProbe(name, label, image string) {
	p.t.Helper()
	var out bytes.Buffer
	args := []string{"run", "-d", "--name", name, "-l", label, image, "sleep", "120"}
	if err := p.h.Run(p.ctx, args, Streams{Stdout: &out, Stderr: &out}); err != nil {
		p.t.Fatalf("run: %v\n%s", err, out.String())
	}
	if err := p.h.WaitContainerRunning(p.ctx, name); err != nil {
		p.t.Fatalf("WaitContainerRunning: %v", err)
	}
}

func (p *probe) findRunning(label string) *config.ContainerDetails {
	p.t.Helper()
	found, err := p.h.FindDevContainer(p.ctx, []string{label})
	if err != nil {
		p.t.Fatalf("FindDevContainer: %v", err)
	}
	if found == nil {
		p.t.Fatal("FindDevContainer returned nil for a running labelled container")
	}
	if found.State.Status != config.ContainerStatusRunning {
		p.t.Errorf("found.State.Status = %q, want %q", found.State.Status, config.ContainerStatusRunning)
	}
	if found.Config.Labels["devsy.e2e"] != "1" {
		p.t.Errorf("label devsy.e2e = %q, want 1", found.Config.Labels["devsy.e2e"])
	}
	return found
}

func (p *probe) execEcho(c *config.ContainerDetails) {
	p.t.Helper()
	var out bytes.Buffer
	if err := p.h.Run(
		p.ctx,
		[]string{"exec", c.ID, "echo", "ok"},
		Streams{Stdout: &out, Stderr: &out},
	); err != nil {
		p.t.Fatalf("exec: %v\n%s", err, out.String())
	}
	if out.String() == "" {
		p.t.Error("exec produced no output")
	}
}

func (p *probe) teardown(c *config.ContainerDetails) {
	p.t.Helper()
	if err := p.h.Stop(p.ctx, c.ID); err != nil {
		p.t.Fatalf("Stop: %v", err)
	}
	// Give the state a moment to settle before delete.
	time.Sleep(time.Second)
	if err := p.h.Remove(p.ctx, c.ID); err != nil {
		p.t.Fatalf("Remove: %v", err)
	}
}

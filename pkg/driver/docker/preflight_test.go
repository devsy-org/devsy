package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
)

func TestDockerPreflightBinaryMissing(t *testing.T) {
	rt, err := docker.RuntimeFromName(string(docker.RuntimeDocker))
	if err != nil {
		t.Fatalf("RuntimeFromName: %v", err)
	}

	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: "devsy-nonexistent-runtime-binary",
			Runtime:       rt,
		},
	}

	err = d.Preflight(context.Background(), driver.PreflightOptions{})
	if err == nil {
		t.Fatal("expected preflight error for missing binary")
	}

	var perr *driver.PreflightError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *driver.PreflightError, got %T", err)
	}
	if perr.Provider != string(docker.RuntimeDocker) {
		t.Fatalf("Provider = %q, want docker", perr.Provider)
	}
}

// installed makes lookPath report the binary as present.
func installed(string) (string, error) { return "/usr/bin/probe", nil }

func alwaysErr(error) func(context.Context) error {
	return func(context.Context) error { return errors.New("boom") }
}

func TestRunPreflightDaemonReachable(t *testing.T) {
	p := dockerProbe{
		runtime:  docker.RuntimeDocker,
		lookPath: installed,
		ping:     func(context.Context) error { return nil },
	}
	if err := runPreflight(context.Background(), driver.PreflightOptions{}, p); err != nil {
		t.Fatalf("reachable daemon = %v, want nil", err)
	}
}

func TestRunPreflightDaemonDownNoAutoStart(t *testing.T) {
	daemonDown := errors.New("Cannot connect to the Docker daemon")
	p := dockerProbe{
		runtime:  docker.RuntimeDocker,
		lookPath: installed,
		ping:     func(context.Context) error { return daemonDown },
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if !errors.Is(err, daemonDown) {
		t.Fatalf("want wrapped daemon error, got %v", err)
	}
}

func TestRunPreflightPodmanAutoStartRecovers(t *testing.T) {
	pinged := 0
	p := dockerProbe{
		runtime:  docker.RuntimePodman,
		lookPath: installed,
		ping: func(context.Context) error {
			pinged++
			if pinged == 1 {
				return errors.New("Cannot connect to Podman")
			}
			return nil // reachable after the machine starts
		},
		start: func(context.Context) error { return nil },
	}
	if err := runPreflight(context.Background(), driver.PreflightOptions{}, p); err != nil {
		t.Fatalf("podman auto-start recovery = %v, want nil", err)
	}
	if pinged != 2 {
		t.Errorf("expected a re-ping after start, ping count = %d", pinged)
	}
}

func TestRunPreflightPodmanAutoStartFails(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	p := dockerProbe{
		runtime:  docker.RuntimePodman,
		lookPath: installed,
		ping:     func(context.Context) error { return down },
		start:    alwaysErr(nil), // start fails; original ping error is surfaced
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if !errors.Is(err, down) {
		t.Fatalf("want original daemon error surfaced, got %v", err)
	}
}

func TestRunPreflightPodmanOptOutSkipsStart(t *testing.T) {
	started := false
	p := dockerProbe{
		runtime:  docker.RuntimePodman,
		lookPath: installed,
		ping:     func(context.Context) error { return errors.New("Cannot connect to Podman") },
		start: func(context.Context) error {
			started = true
			return nil
		},
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{DisableAutoStart: true}, p)
	if err == nil {
		t.Fatal("expected error when opted out and daemon down")
	}
	if started {
		t.Error("auto-start must not run when DisableAutoStart is set")
	}
}

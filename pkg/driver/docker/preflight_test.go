package docker

import (
	"context"
	"errors"
	"os"
	"strings"
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

func TestRunPreflightPodmanSkipsStartWhenNoMachine(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	started := false
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		ping:          func(context.Context) error { return down },
		machineExists: func(context.Context) (bool, error) { return false, nil },
		start: func(context.Context) error {
			started = true
			return nil
		},
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if started {
		t.Error("machine start must not run when no machine exists")
	}
	if !errors.Is(err, down) {
		t.Fatalf("want original daemon error surfaced, got %v", err)
	}
}

func TestRunPreflightPodmanRootfulNoMachineNoRecovery(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	socketStarted := false
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		ping:          func(context.Context) error { return down },
		machineExists: func(context.Context) (bool, error) { return false, nil },
		startSocket: func(context.Context) error {
			socketStarted = true
			return nil
		},
		rootless: false,
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if socketStarted {
		t.Error("rootless socket start must not run for rootful Podman with no machine")
	}
	if !errors.Is(err, down) {
		t.Fatalf("want original daemon error surfaced, got %v", err)
	}
}

func TestRunPreflightPodmanSocketAutoStartRecovers(t *testing.T) {
	pinged := 0
	socketStarted := false
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		machineExists: func(context.Context) (bool, error) { return false, nil },
		ping: func(context.Context) error {
			pinged++
			if pinged == 1 {
				return errors.New("Cannot connect to Podman")
			}
			return nil
		},
		startSocket: func(context.Context) error {
			socketStarted = true
			return nil
		},
		rootless: true,
	}
	if err := runPreflight(context.Background(), driver.PreflightOptions{}, p); err != nil {
		t.Fatalf("socket auto-start recovery = %v, want nil", err)
	}
	if !socketStarted {
		t.Error("expected rootless socket start to be attempted")
	}
	if pinged != 2 {
		t.Errorf("expected a re-ping after socket start, ping count = %d", pinged)
	}
}

func TestRunPreflightPodmanSocketAutoStartFails(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		machineExists: func(context.Context) (bool, error) { return false, nil },
		ping:          func(context.Context) error { return down },
		startSocket:   alwaysErr(errors.New("systemctl: not found")),
		rootless:      true,
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if !errors.Is(err, down) {
		t.Fatalf("want original daemon error surfaced, got %v", err)
	}
	if !strings.Contains(err.Error(), "podman machine is not running") {
		t.Fatalf("want rootless socket hint in error, got %v", err)
	}
}

func TestRunPreflightPodmanNoHintIfMachineExists(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		machineExists: func(context.Context) (bool, error) { return true, nil },
		ping:          func(context.Context) error { return down },
		start:         alwaysErr(errors.New("machine start failed")),
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if strings.Contains(err.Error(), "podman machine is not running") {
		t.Fatalf("want no rootless socket hint when a machine exists, got %v", err)
	}
	if !errors.Is(err, down) {
		t.Fatalf("want original daemon error surfaced, got %v", err)
	}
}

func TestRunPreflightPodmanMachineDetectionFails(t *testing.T) {
	down := errors.New("Cannot connect to Podman")
	listErr := errors.New("machine list: permission denied")
	started := false
	p := dockerProbe{
		runtime:       docker.RuntimePodman,
		lookPath:      installed,
		ping:          func(context.Context) error { return down },
		machineExists: func(context.Context) (bool, error) { return false, listErr },
		start: func(context.Context) error {
			started = true
			return nil
		},
	}
	err := runPreflight(context.Background(), driver.PreflightOptions{}, p)
	if started {
		t.Error("machine start must not run when detection fails")
	}
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

func TestIsRootlessDockerHost(t *testing.T) {
	cases := map[string]bool{
		// explicit rootless socket path
		"unix:///run/user/1001/podman/podman.sock": true,
		"unix:///run/user/1000/podman/podman.sock": true,
		// rootful socket path
		"unix:///run/podman/podman.sock": false,
		// docker / tcp / other hosts are not rootless podman
		"unix:///var/run/docker.sock":             false,
		"tcp://1.2.3.4:2376":                      false,
		"npipe:////./pipe/podman-machine-default": false,
	}
	for host, want := range cases {
		if got := isRootlessDockerHost(host); got != want {
			t.Errorf("isRootlessDockerHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsRootlessDockerHostEmptyFallsBackToUID(t *testing.T) {
	want := os.Geteuid() != 0
	if got := isRootlessDockerHost(""); got != want {
		t.Errorf("isRootlessDockerHost(\"\") = %v, want %v (uid=%d)", got, want, os.Geteuid())
	}
}

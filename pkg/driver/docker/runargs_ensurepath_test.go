package docker

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
)

func TestWindowsToWSLPath(t *testing.T) {
	cases := []struct {
		winPath string
		want    string
	}{
		{`C:\Users\me\repo`, "/mnt/c/Users/me/repo"},
		{`C:\projects\security_dev`, "/mnt/c/projects/security_dev"},
		{`\projects\repo`, "/mnt//projects/repo"},
	}
	for _, tc := range cases {
		t.Run(tc.winPath, func(t *testing.T) {
			if got := windowsToWSLPath(tc.winPath); got != tc.want {
				t.Errorf("windowsToWSLPath(%q) = %q, want %q", tc.winPath, got, tc.want)
			}
		})
	}
}

func TestIsLocalDockerHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", true},
		{"unix:///var/run/docker.sock", true},
		{"npipe:////./pipe/docker_engine", true},
		{"tcp://localhost:2375", false},
		{"ssh://user@localhost", false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			if got := docker.IsLocalDockerHost(tc.host); got != tc.want {
				t.Errorf("IsLocalDockerHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestRemoteDockerHost(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want bool
	}{
		{name: "nil env", env: nil, want: false},
		{name: "no DOCKER_HOST", env: []string{"PATH=/usr/bin"}, want: false},
		{
			name: "unix socket",
			env:  []string{"DOCKER_HOST=unix:///var/run/docker.sock"},
			want: false,
		},
		{name: "npipe", env: []string{"DOCKER_HOST=npipe:////./pipe/docker_engine"}, want: false},
		{name: "tcp", env: []string{"DOCKER_HOST=tcp://localhost:2375"}, want: true},
		{name: "ssh", env: []string{"DOCKER_HOST=ssh://user@localhost"}, want: true},
		{
			name: "ssh with other env first",
			env:  []string{"PATH=/x", "DOCKER_HOST=ssh://u@localhost"},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := docker.RemoteDockerHost(tc.env); got != tc.want {
				t.Errorf("RemoteDockerHost(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func (s *DockerDriverTestSuite) TestEnsurePath_NoopOffWindows() {
	// EnsurePath only converts on Windows; guard that no-op on other OSes.
	s.driver.Docker = &docker.DockerHelper{
		Environment: []string{"DOCKER_HOST=ssh://user@localhost"},
	}
	mount := &config.Mount{Source: `C:\repo`, Target: "/workspace"}
	s.Equal(`C:\repo`, s.driver.EnsurePath(mount).Source)
}

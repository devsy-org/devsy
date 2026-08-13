package microsandbox

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/flags/names"
)

func TestRunArgsFull(t *testing.T) {
	spec := sandboxSpec{
		Image: testImg,
		Entrypoint: []string{
			shPath,
			"-c",
			"sleep infinity",
			"-",
		},
		Env:         map[string]string{"K": "v"},
		Labels:      map[string]string{"devsy.sh/user": "vscode"},
		IdleTimeout: 90 * time.Second,
		BlockEgress: true,
		Memory:      2048,
		MaxMemory:   4096,
		CPUs:        2,
		MaxCPUs:     4,
		Mounts: []volumeMount{
			{Target: "/cache", Volume: "cache-vol"},
			{Target: "/tmp", Tmpfs: true},
		},
	}

	args := runArgs(wsName, spec)

	// "run --detach --name <name>" comes first, image last.
	if len(args) < 4 ||
		args[0] != "run" ||
		args[1] != "--detach" ||
		args[2] != names.Flag(names.Name) ||
		args[3] != wsName {
		t.Errorf("prefix = %v", args[:min(4, len(args))])
	}

	if args[len(args)-1] != testImg {
		t.Errorf("image should be the final arg, got %q", args[len(args)-1])
	}

	want := [][2]string{
		{"--script-raw", "devsy-entrypoint=#!/bin/sh\nsleep infinity"},
		{"--entrypoint", "/.msb/scripts/devsy-entrypoint"},
		{names.Flag(names.Env), "K=v"},
		{"--label", "devsy.sh/user=vscode"},
		{"--idle-timeout", "1m30s"},
		{"--memory", "2048M"},
		{"--max-memory", "4096M"},
		{"--cpus", "2"},
		{"--max-cpus", "4"},
		{"--mount-named", "cache-vol:/cache"},
		{"--tmpfs", "/tmp"},
	}

	for _, kv := range want {
		if !hasFlagValue(args, kv[0], kv[1]) {
			t.Errorf("missing %s %q in %v", kv[0], kv[1], args)
		}
	}

	if !hasFlag(args, "--net-default-egress") {
		t.Errorf("expected egress deny flag in %v", args)
	}
}

func TestRunArgsMinimal(t *testing.T) {
	args := runArgs(wsName, sandboxSpec{Image: testImg})

	// Only run/detach + name + image; no sizing/runtime flags for a bare spec.
	want := []string{
		"run",
		"--detach",
		names.Flag(names.Name),
		wsName,
		testImg,
	}

	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestMountArgsAndNamedVolumes(t *testing.T) {
	mounts := []volumeMount{
		{Target: "/a", Volume: "vol-a"},
		{Target: "/b", Tmpfs: true},
		{Target: "/c", Volume: "vol-c"},
		{Target: testBindDst, Source: testBindSrc},
		{Target: "/ro", Source: "/host/ro", ReadOnly: true},
	}

	if got := namedVolumes(mounts); !slices.Equal(got, []string{"vol-a", "vol-c"}) {
		t.Errorf("namedVolumes = %v, want [vol-a vol-c]", got)
	}

	args := mountArgs(mounts)
	if !hasFlagValue(args, "--mount-named", "vol-a:/a") ||
		!hasFlagValue(args, "--tmpfs", "/b") ||
		!hasFlagValue(args, "--mount-named", "vol-c:/c") ||
		!hasFlagValue(args, "--mount-dir", testBindSrc+":"+testBindDst) ||
		!hasFlagValue(args, "--mount-dir", "/host/ro:/ro:ro") {
		t.Errorf("mountArgs = %v", args)
	}
}

func TestResourceArgsOmitsZero(t *testing.T) {
	if got := resourceArgs(sandboxSpec{}); len(got) != 0 {
		t.Errorf("zero spec should produce no resource args, got %v", got)
	}

	if got := resourceArgs(
		sandboxSpec{Memory: 512},
	); !slices.Equal(
		got,
		[]string{"--memory", "512M"},
	) {
		t.Errorf("resourceArgs = %v", got)
	}
}

func TestRedactArgsMasksEnvValues(t *testing.T) {
	args := []string{
		names.Create,
		names.Flag(names.Env),
		"TOKEN=s3cret",
		"--label",
		"k=v",
		names.Flag(names.Env),
		"PLAIN=ok",
	}

	got := redactArgs(args)

	if strings.Contains(got, "s3cret") || strings.Contains(got, "ok") {
		t.Errorf("env values leaked: %q", got)
	}

	if !strings.Contains(got, "TOKEN=***") ||
		!strings.Contains(got, "PLAIN=***") {
		t.Errorf("env keys should be preserved with masked values: %q", got)
	}

	if !strings.Contains(got, "--label k=v") {
		t.Errorf("non-env args should be untouched: %q", got)
	}
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

package microsandbox

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/flags/names"
)

func runPrefix() []string {
	return []string{msbCmdRun, msbFlagDetach, names.Flag(names.Name), wsName}
}

func TestRunArgsFull(t *testing.T) {
	spec := sandboxSpec{
		Image:       testImg,
		Entrypoint:  shPath,
		Cmd:         []string{"-c", "sleep infinity", "-"},
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

	if !slices.Equal(args[:4], runPrefix()) {
		t.Errorf("prefix = %v, want %v", args[:4], runPrefix())
	}

	imgIdx := slices.Index(args, testImg)
	if imgIdx < 0 {
		t.Fatalf("image not found in %v", args)
	}
	wantSuffix := append([]string{testImg, "--"}, spec.Cmd...)
	if !slices.Equal(args[imgIdx:], wantSuffix) {
		t.Errorf("image+cmd suffix = %v, want %v", args[imgIdx:], wantSuffix)
	}

	wantFlags := [][2]string{
		{"--entrypoint", shPath},
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
	for _, kv := range wantFlags {
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
	want := append(runPrefix(), testImg)
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestRunArgsCmdWithoutEntrypoint(t *testing.T) {
	args := runArgs(wsName, sandboxSpec{Image: testImg, Cmd: []string{"python3", "worker.py"}})
	if hasFlag(args, "--entrypoint") {
		t.Errorf("did not expect --entrypoint in %v", args)
	}
	want := append(runPrefix(), testImg, "--", "python3", "worker.py")
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestRunArgsEntrypointWithoutCmd(t *testing.T) {
	args := runArgs(wsName, sandboxSpec{Image: testImg, Entrypoint: shPath})
	want := append(runPrefix(), "--entrypoint", shPath, testImg)
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

func TestResourceArgsRootDisk(t *testing.T) {
	if got := resourceArgs(sandboxSpec{RootDiskGB: 32}); !slices.Equal(
		got,
		[]string{flagRootDisk, "32G"},
	) {
		t.Errorf("resourceArgs = %v", got)
	}
	if got := resourceArgs(sandboxSpec{}); slices.Contains(got, flagRootDisk) {
		t.Errorf("zero RootDiskGB should omit --root-disk, got %v", got)
	}
}

func TestResourceArgsEphemeralUsesTmpfsRootDisk(t *testing.T) {
	if got := resourceArgs(sandboxSpec{Ephemeral: true, RootDiskGB: 32}); !slices.Equal(
		got,
		[]string{flagRootDisk, "tmpfs:32G"},
	) {
		t.Errorf("resourceArgs = %v", got)
	}
}

func TestResourceArgsEphemeralWithoutSizeUsesDefault(t *testing.T) {
	if got := resourceArgs(sandboxSpec{Ephemeral: true}); !slices.Equal(
		got,
		[]string{flagRootDisk, fmt.Sprintf("tmpfs:%dG", defaultEphemeralRootDiskGB)},
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
	if !strings.Contains(got, "TOKEN=***") || !strings.Contains(got, "PLAIN=***") {
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

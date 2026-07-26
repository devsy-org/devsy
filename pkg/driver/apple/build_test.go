package apple

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
)

func TestBuildArgs(t *testing.T) {
	opts := &build.BuildOptions{
		Dockerfile: "/tmp/Dockerfile",
		Context:    "/tmp/ctx",
		Images:     []string{"img:a", "img:b"},
		BuildArgs:  map[string]string{"B": "2", "A": "1"},
		Labels:     map[string]string{"z": "9", "a": "0"},
		Target:     "dev",
		NoCache:    true,
		CliOpts:    []string{"--extra"},
	}

	args := buildArgs(opts, "linux/arm64")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"build -f /tmp/Dockerfile",
		"--no-cache",
		"-t img:a", "-t img:b",
		"--build-arg A=1", "--build-arg B=2",
		"--label a=0", "--label z=9",
		"--target dev",
		"--platform linux/arm64",
		"--extra",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("buildArgs missing %q\n got: %s", want, joined)
		}
	}

	// build-arg keys must be sorted deterministically (A before B).
	if strings.Index(joined, "A=1") > strings.Index(joined, "B=2") {
		t.Errorf("build-arg keys not sorted: %s", joined)
	}
	// Context must be the final argument.
	if args[len(args)-1] != "/tmp/ctx" {
		t.Errorf("context must be last, got %q", args[len(args)-1])
	}
}

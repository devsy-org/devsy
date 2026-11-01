package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const detectorVersion = "v0.10.0"

const (
	archAMD64 = "amd64"
	archARM64 = "arm64"
)

var releaseTargets = []struct{ os, arch string }{
	{"linux", archAMD64},
	{"linux", archARM64},
	{"darwin", archAMD64},
	{"darwin", archARM64},
	{"windows", archAMD64},
	{"windows", archARM64},
}

// commandRunner starts a subprocess. execCommand is used in production; tests
// substitute a fake so that neither go nor git runs.
type commandRunner func(cmd *exec.Cmd) error

func execCommand(cmd *exec.Cmd) error {
	return cmd.Run()
}

// generator carries the repository root and the process seams for one run.
type generator struct {
	root   string
	runCmd commandRunner
	stderr io.Writer
}

func main() {
	check := flag.Bool("check", false,
		"verify THIRD_PARTY_LICENSES.md is in sync instead of writing it")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := repoRoot(cwd)
	if err != nil {
		return err
	}
	g := generator{root: root, runCmd: execCommand, stderr: os.Stderr}
	return g.generate(check)
}

func (g generator) generate(check bool) error {
	hackDir := filepath.Join(g.root, "hack", "licenses")
	outFile := filepath.Join(g.root, "THIRD_PARTY_LICENSES.md")

	deps, err := g.moduleListJSON()
	if err != nil {
		return fmt.Errorf("collecting module dependencies: %w", err)
	}

	if check {
		return g.checkInSync(hackDir, outFile, deps)
	}

	_, _ = fmt.Fprintln(g.stderr, "Generating", filepath.Base(outFile)+"...")
	if err := g.runDetector(hackDir, deps, outFile); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(g.stderr, "Wrote", outFile)
	return nil
}

// repoRoot returns the nearest directory at or above dir that contains go.mod.
func repoRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate go.mod above %q", dir)
		}
		dir = parent
	}
}

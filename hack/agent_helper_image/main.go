// Cross-compiles the agent-helper volume helper for each published arch into
// images/agent-helper/dist, producing the per-arch binaries the FROM scratch
// image copies in via TARGETARCH.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type target struct {
	zig string // Zig -target triple
	out string // output name, matching the Dockerfile's TARGETARCH
}

var targets = []target{
	{zig: "x86_64-linux-musl", out: "helper-amd64"},
	{zig: "aarch64-linux-musl", out: "helper-arm64"},
}

func main() {
	dir := flag.String("dir", "images/agent-helper", "agent-helper image directory")
	flag.Parse()

	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "agent_helper_image:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	distDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}

	src := filepath.Join(dir, "helper.zig")
	for _, t := range targets {
		out := filepath.Join(distDir, t.out)
		cmd := exec.Command("zig", "build-exe", src,
			"-target", t.zig, "-O", "ReleaseSmall", "-femit-bin="+out,
			"--cache-dir", filepath.Join(distDir, ".zig-cache"))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", t.out, err)
		}

		info, err := os.Stat(out)
		if err != nil {
			return err
		}
		fmt.Printf("built %s (%d bytes)\n", out, info.Size())
	}
	return nil
}

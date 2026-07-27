// Cross-compiles the agent-helper volume helper for each published arch into
// images/agent-helper/dist, producing the per-arch binaries the FROM scratch
// image copies in via TARGETARCH.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	args := os.Args[1:]
	cmd := "build"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "build":
		err = runBuild(args)
	case "version":
		err = runVersion(args)
	case "exists":
		err = runExists(args)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent_helper_image:", err)
		os.Exit(1)
	}
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dir := fs.String("dir", "images/agent-helper", "agent-helper image directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return build(*dir)
}

func build(dir string) error {
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

func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	dir := fs.String("dir", "images/agent-helper", "agent-helper image directory")
	tag := fs.String("tag", "", "explicit version override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	version := strings.TrimSpace(*tag)
	if version == "" {
		data, err := os.ReadFile(filepath.Join(*dir, "VERSION"))
		if err != nil {
			return err
		}
		version = strings.TrimSpace(string(data))
	}
	if version == "" {
		return fmt.Errorf("agent-helper version is empty")
	}

	fmt.Println(version)
	return writeOutput("value", version)
}

func runExists(args []string) error {
	fs := flag.NewFlagSet("exists", flag.ExitOnError)
	image := fs.String("image", "", "image repository (without tag)")
	tag := fs.String("tag", "", "image tag to check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *image == "" || *tag == "" {
		return fmt.Errorf("both -image and -tag are required")
	}

	ref := *image + ":" + *tag
	exists, err := manifestExists(ref)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("::notice::%s already exists; skipping push\n", ref)
	}
	return writeOutput("value", strconv.FormatBool(exists))
}

func manifestExists(ref string) (bool, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("docker", "manifest", "inspect", ref)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if isAbsent(msg) {
			return false, nil
		}
		return false, fmt.Errorf(
			"docker manifest inspect %s: %w: %s", ref, err, msg,
		)
	}
	return true, nil
}

func isAbsent(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"no such manifest",
		"manifest unknown",
		"name unknown",
		"not found",
		"denied",
		"unauthorized",
		"forbidden",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func writeOutput(key, value string) error {
	out := os.Getenv("GITHUB_OUTPUT")
	if out == "" {
		return nil
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "%s=%s\n", key, value)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

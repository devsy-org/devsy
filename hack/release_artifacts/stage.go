package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func cmdStage(args []string) error {
	fs := flag.NewFlagSet("stage", flag.ExitOnError)
	srcDir := fs.String("src-dir", "", "directory containing the per-arch CLI binaries")
	dstDir := fs.String("dst-dir", "", "directory the embedded binary is copied into")
	goos := fs.String("goos", "", "GOOS for the binary to stage")
	goarch := fs.String("goarch", "", "GOARCH for the binary to stage")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *srcDir == "" || *dstDir == "" || *goos == "" || *goarch == "" {
		return errors.New("stage: missing required flag (-src-dir, -dst-dir, -goos, -goarch)")
	}
	return runStage(*srcDir, *dstDir, *goos, *goarch)
}

func runStage(srcDir, dstDir, goos, goarch string) error {
	srcName := fmt.Sprintf("devsy-%s-%s", goos, goarch)
	dstName := "devsy"
	if goos == OSWindows {
		srcName += ".exe"
		dstName = "devsy.exe"
	}
	src := filepath.Join(srcDir, srcName)
	dst := filepath.Join(dstDir, dstName)

	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("expected CLI binary missing: %s: %w", src, err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil { // #nosec G301 -- release tooling.
		return fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	if err := copyExecutable(src, dst); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	fmt.Printf("staged %s -> %s\n", src, dst)
	return verify(dst, Arch{GOOS: goos, GOARCH: goarch})
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- caller-controlled release tooling.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	const flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	out, err := os.OpenFile(dst, flags, 0o755) // #nosec G302,G304 -- release tooling.
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func stageCmd() *cobra.Command {
	var srcDir, dstDir, goos, goarch string
	cmd := &cobra.Command{
		Use:   "stage",
		Short: "copy the matching per-arch CLI binary into --dst-dir and verify its arch",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runStage(srcDir, dstDir, goos, goarch)
		},
	}
	cmd.Flags().StringVar(&srcDir, "src-dir", "", "directory containing the per-arch CLI binaries")
	cmd.Flags().StringVar(&dstDir, "dst-dir", "", "directory the embedded binary is copied into")
	cmd.Flags().StringVar(&goos, "goos", "", "GOOS for the binary to stage")
	cmd.Flags().StringVar(&goarch, "goarch", "", "GOARCH for the binary to stage")
	for _, f := range []string{"src-dir", "dst-dir", "goos", "goarch"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
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

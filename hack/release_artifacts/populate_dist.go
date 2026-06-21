package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func populateDistCmd() *cobra.Command {
	var srcDir, dstDir string
	cmd := &cobra.Command{
		Use:   "populate-dist",
		Short: "copy every devsy-* binary from --src-dir into --dst-dir",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runPopulateDist(srcDir, dstDir)
		},
	}
	cmd.Flags().StringVar(&srcDir, "src-dir", "", "directory containing the per-arch CLI binaries")
	cmd.Flags().StringVar(&dstDir, "dst-dir", "", "directory to populate")
	for _, f := range []string{"src-dir", "dst-dir"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func runPopulateDist(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil { // #nosec G301 -- release tooling.
		return fmt.Errorf("mkdir %s: %w", dstDir, err)
	}
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), "devsy-") {
			return nil
		}
		return copyExecutable(p, filepath.Join(dstDir, d.Name()))
	}
	return filepath.WalkDir(srcDir, walk)
}

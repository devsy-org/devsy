package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

func populateDistCmd() *cli.Command {
	return &cli.Command{
		Name:  "populate-dist",
		Usage: "copy every devsy-* binary from -src-dir into -dst-dir",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "src-dir",
				Usage:    "directory containing the per-arch CLI binaries",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "dst-dir",
				Usage:    "directory to populate",
				Required: true,
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return runPopulateDist(c.String("src-dir"), c.String("dst-dir"))
		},
	}
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

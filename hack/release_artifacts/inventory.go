package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/urfave/cli/v3"
)

func inventoryCmd() *cli.Command {
	return &cli.Command{
		Name:  "inventory",
		Usage: "log size/sha256/arch for each file under -dir; optionally flatten",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Usage: "directory to inventory", Required: true},
			&cli.BoolFlag{
				Name:  "flatten",
				Usage: "after inventory, move nested files into <dir> root and remove empty subdirs",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return runInventory(c.String("dir"), c.Bool("flatten"))
		},
	}
}

func runInventory(dir string, flatten bool) error {
	files, err := walkFiles(dir)
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}
	if err := printGroup("CLI artifact inventory", dir, files); err != nil {
		return err
	}
	if !flatten {
		return nil
	}
	if err := flattenInto(dir, files); err != nil {
		return fmt.Errorf("flatten: %w", err)
	}
	if err := removeEmptyDirs(dir); err != nil {
		return fmt.Errorf("cleanup empty dirs: %w", err)
	}
	flat, err := walkFiles(dir)
	if err != nil {
		return fmt.Errorf("post-flatten walk: %w", err)
	}
	return printGroup("Flattened CLI binaries", dir, flat)
}

func printGroup(label, root string, files []string) error {
	fmt.Printf("::group::%s\n", label)
	defer fmt.Println("::endgroup::")
	for _, p := range files {
		if err := printEntry(root, p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

func walkFiles(root string) ([]string, error) {
	var out []string
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func printEntry(root, p string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return err
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	sum, err := sha256File(p)
	if err != nil {
		return err
	}
	archStr := "<undetected>"
	if a, err := FromFile(p); err == nil {
		archStr = a.String()
	}
	fmt.Printf("%s  size=%d  sha256=%s  arch=%s\n", rel, st.Size(), sum, archStr)
	return nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p) // #nosec G304 -- caller-controlled release tooling.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// flattenInto fails on name collisions instead of silently overwriting.
func flattenInto(root string, files []string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, p := range files {
		parent, err := filepath.Abs(filepath.Dir(p))
		if err != nil {
			return err
		}
		if parent == rootAbs {
			continue
		}
		dest := filepath.Join(rootAbs, filepath.Base(p))
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("flatten collision: %s -> %s already exists", p, dest)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.Rename(p, dest); err != nil {
			return err
		}
	}
	return nil
}

func removeEmptyDirs(root string) error {
	var dirs []string
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return err
	}
	// Deepest first so parents become removable after their children go.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		_ = os.Remove(d)
	}
	return nil
}

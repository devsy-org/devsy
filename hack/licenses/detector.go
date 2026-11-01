package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// checkInSync regenerates the report into a temp file and compares it with
// outFile. An out-of-date file prints a diff and a warning but returns nil.
func (g generator) checkInSync(hackDir, outFile string, deps []byte) error {
	tmp, err := os.CreateTemp("", "third-party-licenses-*.md")
	if err != nil {
		return err
	}
	target := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(target) }()

	_, _ = fmt.Fprintln(g.stderr, "Generating", filepath.Base(outFile)+"...")
	if err := g.runDetector(hackDir, deps, target); err != nil {
		return err
	}

	current, err := os.ReadFile(outFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", outFile, err)
	}
	generated, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, generated) {
		diff := exec.Command("git", "--no-pager", "diff", "--no-index", "--", outFile, target)
		diff.Stdout = g.stderr
		diff.Stderr = g.stderr
		_ = g.runCmd(diff)
		_, _ = fmt.Fprintln(
			g.stderr,
			"warning: THIRD_PARTY_LICENSES.md is out of date; run 'mise run cli:licenses' and commit the result",
		)
		return nil
	}

	_, _ = fmt.Fprintln(g.stderr, "THIRD_PARTY_LICENSES.md is up to date.")
	return nil
}

func (g generator) runDetector(hackDir string, deps []byte, outPath string) error {
	tmpDir, err := os.MkdirTemp("", "go-licence-detector-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cmd := exec.Command("go", "run",
		"go.elastic.co/go-licence-detector@"+detectorVersion,
		"-rules", filepath.Join(hackDir, "rules.json"),
		"-overrides", filepath.Join(hackDir, "overrides.ndjson"),
		"-depsTemplate", filepath.Join(hackDir, "third-party-licenses.md.tmpl"),
		"-depsOut", outPath,
	)
	cmd.Dir = tmpDir
	cmd.Stdin = bytes.NewReader(deps)
	cmd.Stderr = g.stderr
	if err := g.runCmd(cmd); err != nil {
		return fmt.Errorf("go-licence-detector: %w", err)
	}

	return normalizeTrailingNewline(outPath)
}

// normalizeTrailingNewline leaves exactly one trailing newline on path.
func normalizeTrailingNewline(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	normalized := append(bytes.TrimRight(content, "\n"), '\n')
	if bytes.Equal(content, normalized) {
		return nil
	}
	return os.WriteFile(path, normalized, 0o644)
}

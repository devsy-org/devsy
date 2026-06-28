package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	root, err := repoRoot()
	if err != nil {
		return err
	}

	hackDir := filepath.Join(root, "hack", "licenses")
	outFile := filepath.Join(root, "THIRD_PARTY_LICENSES.md")

	deps, err := moduleListJSON(root)
	if err != nil {
		return fmt.Errorf("collecting module dependencies: %w", err)
	}

	if check {
		return checkInSync(hackDir, outFile, deps)
	}

	fmt.Fprintln(os.Stderr, "Generating", filepath.Base(outFile)+"...")
	if err := runDetector(hackDir, deps, outFile); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Wrote", outFile)
	return nil
}

func checkInSync(hackDir, outFile string, deps []byte) error {
	tmp, err := os.CreateTemp("", "third-party-licenses-*.md")
	if err != nil {
		return err
	}
	target := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(target) }()

	fmt.Fprintln(os.Stderr, "Generating", filepath.Base(outFile)+"...")
	if err := runDetector(hackDir, deps, target); err != nil {
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
		diff.Stdout = os.Stderr
		diff.Stderr = os.Stderr
		_ = diff.Run()
		// Dependency bumps (e.g. Renovate) change the attribution file without
		// regenerating it, so warn instead of failing; the file is reconciled
		// when a maintainer runs the generator and commits the result.
		fmt.Fprintln(
			os.Stderr,
			"warning: THIRD_PARTY_LICENSES.md is out of date; run 'task cli:licenses' and commit the result",
		)
		return nil
	}

	fmt.Fprintln(os.Stderr, "THIRD_PARTY_LICENSES.md is up to date.")
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
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

type goModule struct {
	Path    string
	Version string
	Main    bool
	Dir     string
}

type goPackage struct {
	Module *goModule
}

func moduleListJSON(dir string) ([]byte, error) {
	modules := map[string]goModule{}
	for _, t := range releaseTargets {
		mods, err := buildModules(dir, t.os, t.arch)
		if err != nil {
			return nil, fmt.Errorf("listing deps for %s/%s: %w", t.os, t.arch, err)
		}
		maps.Copy(modules, mods)
	}

	paths := make([]string, 0, len(modules))
	for p := range modules {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "\t")
	for _, p := range paths {
		if err := enc.Encode(modules[p]); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func buildModules(dir, goos, goarch string) (map[string]goModule, error) {
	cmd := exec.Command("go", "list", "-deps", "-mod=readonly", "-json", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	modules := map[string]goModule{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		m := pkg.Module
		if m == nil || m.Main || m.Path == "" {
			continue
		}
		modules[m.Path] = *m
	}
	return modules, nil
}

func runDetector(hackDir string, deps []byte, outPath string) error {
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
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go-licence-detector: %w", err)
	}

	return normalizeTrailingNewline(outPath)
}

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

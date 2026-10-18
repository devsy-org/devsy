package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"sort"
)

type goModule struct {
	Path    string
	Version string
	Main    bool
	Dir     string
}

type goPackage struct {
	Module *goModule
}

// moduleListJSON merges third-party modules from every release target and
// returns them as the sorted, indented JSON stream fed to the detector.
func (g generator) moduleListJSON() ([]byte, error) {
	modules := map[string]goModule{}
	for _, t := range releaseTargets {
		mods, err := g.buildModules(t.os, t.arch)
		if err != nil {
			return nil, fmt.Errorf("listing deps for %s/%s: %w", t.os, t.arch, err)
		}
		maps.Copy(modules, mods)
	}
	return encodeModules(modules)
}

// encodeModules writes modules sorted by path, one indented JSON object each.
func encodeModules(modules map[string]goModule) ([]byte, error) {
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

func (g generator) buildModules(goos, goarch string) (map[string]goModule, error) {
	cmd := exec.Command("go", "list", "-deps", "-mod=readonly", "-json", "./...")
	cmd.Dir = g.root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = g.stderr
	if err := g.runCmd(cmd); err != nil {
		return nil, err
	}
	return decodeModules(&out)
}

// decodeModules reads a `go list -json` object stream and keeps third-party
// modules keyed by path. Packages without module info, main-module entries,
// and entries with an empty path are skipped.
func decodeModules(r io.Reader) (map[string]goModule, error) {
	modules := map[string]goModule{}
	dec := json.NewDecoder(r)
	for {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
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

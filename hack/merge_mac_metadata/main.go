// Merge macOS electron-updater metadata from separate arch builds.
//
// When building macOS arm64 and x64 separately, each produces its own
// latest-mac.yml (or beta-mac.yml) with only one architecture's files.
// This script merges them into a single file with entries for both arches.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func findFiles(root, name string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return found, nil
}

func loadYAML(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return data, nil
}

func mergeFileEntries(paths []string) (base map[string]any, files []any, err error) {
	seen := map[string]int{} // url -> index in files
	for _, p := range paths {
		data, err := loadYAML(p)
		if err != nil {
			return nil, nil, err
		}
		if data == nil {
			continue
		}
		if base == nil {
			base = data
		}
		entries, ok := data["files"].([]any)
		if !ok {
			continue
		}
		for _, e := range entries {
			// Defensive: electron-builder always emits maps; skip non-map entries
			// (prior behavior appended raw entries via variadic spread).
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			url, _ := entry["url"].(string)
			if url == "" {
				files = append(files, entry)
				continue
			}
			if idx, exists := seen[url]; exists {
				files[idx] = entry // last-write-wins
				continue
			}
			seen[url] = len(files)
			files = append(files, entry)
		}
	}
	return base, files, nil
}

// applyTopLevelFromFirst sets the top-level path/sha512/size from the
// files[] entry that matches the base file's own preferred path when
// possible, falling back to the first entry. This avoids electron-updater
// fallback consumers (e.g. Rosetta) downloading the wrong-arch binary
// just because dedup iteration happened to land arm64 before x64.
func applyTopLevelFromFirst(base map[string]any, files []any) {
	if len(files) == 0 {
		return
	}
	preferred := pickPreferred(base, files)
	base["path"] = preferred["url"]
	base["sha512"] = preferred["sha512"]
	base["size"] = preferred["size"]
}

func pickPreferred(base map[string]any, files []any) map[string]any {
	if want, ok := base["path"].(string); ok && want != "" {
		for _, e := range files {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if u, ok := entry["url"].(string); ok && u == want {
				return entry
			}
		}
	}
	if first, ok := files[0].(map[string]any); ok {
		return first
	}
	return map[string]any{}
}

func writeYAML(path string, data map[string]any) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func mergePrefix(metadataDir, outputDir, prefix string) error {
	target := prefix + ".yml"
	found, err := findFiles(metadataDir, target)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}

	base, files, err := mergeFileEntries(found)
	if err != nil {
		return err
	}
	if base == nil {
		return nil
	}

	base["files"] = files
	applyTopLevelFromFirst(base, files)

	outFile := filepath.Join(outputDir, target)
	if err := writeYAML(outFile, base); err != nil {
		return err
	}
	fmt.Printf("Merged %d files into %s\n", len(found), outFile)
	return nil
}

func mergeMacFiles(metadataDir, outputDir string) error {
	for _, prefix := range []string{"latest-mac", "beta-mac"} {
		if err := mergePrefix(metadataDir, outputDir, prefix); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <metadata_dir> <output_dir>\n", os.Args[0])
		os.Exit(2)
	}
	if err := mergeMacFiles(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

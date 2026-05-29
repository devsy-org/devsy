// Rewrites relative file URLs in electron-updater manifests to absolute
// GitHub Release asset URLs so binaries can live on GitHub Releases while
// the manifests are served from a CDN.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func isAbsolute(u string) bool {
	lu := strings.ToLower(u)
	return strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://")
}

func absURL(repo, tag, filename string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, filename)
}

func rewriteFilesEntries(data map[string]any, repo, tag string) {
	entries, ok := data["files"].([]any)
	if !ok {
		return
	}
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		u, ok := entry["url"].(string)
		if !ok || isAbsolute(u) {
			continue
		}
		entry["url"] = absURL(repo, tag, u)
	}
}

func rewritePath(data map[string]any, repo, tag string) {
	p, ok := data["path"].(string)
	if !ok || isAbsolute(p) {
		return
	}
	data["path"] = absURL(repo, tag, p)
}

func rewriteFile(path, repo, tag string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	rewriteFilesEntries(data, repo, tag)
	rewritePath(data, repo, tag)
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0o644)
}

func walkAndRewrite(root, repo, tag string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yml") {
			return nil
		}
		if !strings.HasPrefix(name, "latest") && !strings.HasPrefix(name, "beta") {
			return nil
		}
		if err := rewriteFile(path, repo, tag); err != nil {
			return err
		}
		fmt.Printf("rewrote %s\n", path)
		return nil
	})
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: %s <dir> <owner/repo> <tag>\n", os.Args[0])
		os.Exit(2)
	}
	dir, repo, tag := os.Args[1], os.Args[2], os.Args[3]
	if repo == "" || tag == "" {
		fmt.Fprintf(os.Stderr, "owner/repo and tag must be non-empty (repo=%q tag=%q)\n", repo, tag)
		os.Exit(2)
	}
	if err := walkAndRewrite(dir, repo, tag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

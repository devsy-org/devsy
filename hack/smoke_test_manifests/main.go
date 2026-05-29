// Smoke-tests electron-updater manifests by HEAD-checking every URL.
// Fails on any non-2xx response or unreachable URL. Reads GITHUB_TOKEN
// from the environment to authenticate requests to github.com (so
// draft release assets are reachable).
package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func isGitHubHost(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	// Only send the GITHUB_TOKEN over https to avoid leaking it via http.
	if parsed.Scheme != "https" {
		return false
	}
	host := parsed.Hostname()
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}

type failure struct {
	url    string
	reason string
}

func extractFilesURLs(data map[string]any) []string {
	entries, ok := data["files"].([]any)
	if !ok {
		return nil
	}
	var urls []string
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if u, ok := entry["url"].(string); ok && u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

func collectURLs(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	urls := extractFilesURLs(data)
	if top, ok := data["path"].(string); ok && top != "" {
		urls = append(urls, top)
	}
	return urls, nil
}

func checkURL(client *http.Client, u, token string) *failure {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return &failure{u, "relative"}
	}
	req, err := http.NewRequest(http.MethodHead, u, nil)
	if err != nil {
		return &failure{u, err.Error()}
	}
	if token != "" && isGitHubHost(u) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &failure{u, err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return &failure{u, fmt.Sprintf("%d", resp.StatusCode)}
	}
	return nil
}

func checkManifest(client *http.Client, path, token string) []failure {
	urls, err := collectURLs(path)
	if err != nil {
		return []failure{{path, err.Error()}}
	}
	if len(urls) == 0 {
		fmt.Printf("WARN: no urls in %s\n", path)
		return nil
	}
	var failures []failure
	for _, u := range urls {
		if f := checkURL(client, u, token); f != nil {
			failures = append(failures, *f)
		}
	}
	return failures
}

func run(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return fmt.Errorf("glob %s: %w", dir, err)
	}
	if len(matches) == 0 {
		fmt.Printf("no manifests found in %s\n", dir)
		return nil
	}
	token := os.Getenv("GITHUB_TOKEN")
	client := &http.Client{Timeout: 20 * time.Second}
	var allFailures []failure
	for _, m := range matches {
		fmt.Printf("checking %s\n", m)
		allFailures = append(allFailures, checkManifest(client, m, token)...)
	}
	if len(allFailures) > 0 {
		for _, f := range allFailures {
			fmt.Printf("FAIL %s: %s\n", f.url, f.reason)
		}
		return fmt.Errorf("%d url(s) failed smoke test", len(allFailures))
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <manifest_dir>\n", os.Args[0])
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

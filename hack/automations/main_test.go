package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newSandbox copies the generator's real inputs (agents.yaml and the existing
// .agents/agents tree) into an isolated temp directory and returns its path.
// The generator writes agent.md files relative to its working directory, and
// goreleaser's build hooks run this test suite once per target arch in
// parallel; without isolation, concurrent runs race on the same real
// .agents/agents/*/agent.md files in the checkout.
func newSandbox(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()

	copyFile(t,
		filepath.Join(root, "hack", "automations", "agents.yaml"),
		filepath.Join(dir, "hack", "automations", "agents.yaml"))
	copyTree(t,
		filepath.Join(root, ".agents", "agents"),
		filepath.Join(dir, ".agents", "agents"))

	return dir
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy tree %s: %v", src, err)
	}
}

func runGenerator(t *testing.T, dir string, args ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "automations")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("generator output:\n%s", out)
	}
	return string(out)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func agentFilePath(dir, id string) string {
	return filepath.Join(dir, ".agents", "agents", id, "agent.md")
}

func TestGenerateWritesAllAgents(t *testing.T) {
	dir := newSandbox(t)
	out := runGenerator(t, dir)
	if !strings.Contains(out, "wrote") {
		t.Fatalf("expected write output, got:\n%s", out)
	}
	ids := []string{
		"ci-optimizer", "cmd-reviewer", "devcontainer-spec", "docs-keeper",
		"integration-test", "lint-fixer", "pkg-container", "pkg-core-agent",
		"pkg-ssh-git", "pkg-system-platform", "pkg-tunnel-network",
		"agent-analytics",
	}

	pythonAgents := map[string]bool{"agent-analytics": true}
	for _, id := range ids {
		p := agentFilePath(dir, id)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("read %s: %v", p, err)
			continue
		}
		s := string(b)
		for _, want := range []string{
			"Do NOT run `git commit`",
			"verified=true",
			"sign_commit",
			"-pr-only",
			"---\nid:",
			"## Scope",
			"## Self-improvement",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", id, want)
			}
		}
		if strings.Contains(s, "app_commit.py") {
			t.Errorf("%s: still references app_commit.py", id)
		}
		if strings.Contains(s, "pip install") {
			t.Errorf("%s: still uses pip install; use uv run with PEP 723 inline deps", id)
		}
		if strings.Contains(s, "python3 -m venv") || strings.Contains(s, "venv/bin/python") {
			t.Errorf("%s: still uses a venv; use uv run only", id)
		}
		if pythonAgents[id] && !strings.Contains(s, "uv run hack/analytics/analyze_runs.py") {
			t.Errorf("%s: expected uv run invocation for the analytics script", id)
		}
	}
}

func TestCheckIsIdempotent(t *testing.T) {
	dir := newSandbox(t)
	runGenerator(t, dir)
	out := runGenerator(t, dir, "-check")
	if strings.Contains(out, "DRIFT") {
		t.Fatalf("freshly generated files report drift:\n%s", out)
	}
}

func TestToolchainPins(t *testing.T) {
	dir := newSandbox(t)
	runGenerator(t, dir)
	goAgent := agentFilePath(dir, "pkg-container")
	b, err := os.ReadFile(goAgent)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"go1.26.3", "golangci-lint-2.12.2", "go-task/task/v3"} {
		if !strings.Contains(s, want) {
			t.Errorf("pkg-container: missing pinned %q", want)
		}
	}
	ciAgent := agentFilePath(dir, "ci-optimizer")
	cb, err := os.ReadFile(ciAgent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cb), "go.dev/dl/go") {
		t.Errorf("ci-optimizer: should not install Go toolchain (toolchain=gh+act)")
	}
}

func TestToolchainDownloadsAreBounded(t *testing.T) {
	dir := newSandbox(t)
	runGenerator(t, dir)
	for _, id := range []string{"ui-polish", "pkg-container", "agent-analytics"} {
		p := agentFilePath(dir, id)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(b)
		if !strings.Contains(s, "STEP 0") {
			continue
		}
		for _, bad := range []string{
			"curl -sSL https://go.dev/dl",
			"curl -fsSL https://deb.nodesource.com",
			"curl -sSL https://github.com/golangci",
		} {
			if strings.Contains(s, bad) {
				t.Errorf("%s: unbounded curl download remains: %q", id, bad)
			}
		}
		if !strings.Contains(s, "--max-time 300") {
			t.Errorf("%s: curl downloads must set --max-time", id)
		}
		if !strings.Contains(s, "echo \"installing go 1.26.3\"") {
			t.Errorf("%s: go install must echo progress for observability", id)
		}
	}
}

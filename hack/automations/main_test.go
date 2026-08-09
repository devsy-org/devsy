package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGenerator(t *testing.T, args ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "automations")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = repoRoot(t)
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

func agentFilePath(t *testing.T, id string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".agents", "agents", id, "agent.md")
}

func TestGenerateWritesAllAgents(t *testing.T) {
	out := runGenerator(t)
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
		p := agentFilePath(t, id)
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
	runGenerator(t)
	out := runGenerator(t, "-check")
	if strings.Contains(out, "DRIFT") {
		t.Fatalf("freshly generated files report drift:\n%s", out)
	}
}

func TestToolchainPins(t *testing.T) {
	runGenerator(t)
	goAgent := agentFilePath(t, "pkg-container")
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
	ciAgent := agentFilePath(t, "ci-optimizer")
	cb, err := os.ReadFile(ciAgent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cb), "go.dev/dl/go") {
		t.Errorf("ci-optimizer: should not install Go toolchain (toolchain=gh+act)")
	}
}

func TestToolchainDownloadsAreBounded(t *testing.T) {
	runGenerator(t)
	for _, id := range []string{"ui-polish", "pkg-container", "agent-analytics"} {
		p := agentFilePath(t, id)
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

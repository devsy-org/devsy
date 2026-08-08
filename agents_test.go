package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var frontmatterRe = regexp.MustCompile(`(?s)^\s*---\s*\n(.*?)\n---\s*\n`)

func agentsDir(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(root, ".agents")
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- path is a test fixture under .agents
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func parseFrontmatter(body string) (map[string]string, bool) {
	m := frontmatterRe.FindStringSubmatch(body)
	if m == nil {
		return nil, false
	}
	out := make(map[string]string)
	for line := range strings.SplitSeq(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(val)
	}
	return out, true
}

func requireKeys(t *testing.T, fm map[string]string, path string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := fm[strings.ToLower(k)]; !ok || strings.TrimSpace(v) == "" {
			t.Errorf("%s: front matter missing required field %q", path, k)
		}
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func validateAgent(t *testing.T, root, id string) {
	path := filepath.Join(root, "agents", id, "agent.md")
	fm, ok := parseFrontmatter(mustRead(t, path))
	if !ok {
		t.Errorf("%s: missing front matter", path)
		return
	}
	requireKeys(t, fm, path, "id", "name", "description", "enabled")
	if fm["id"] != id {
		t.Errorf("%s: id %q != dir name %q", path, fm["id"], id)
	}
	if fm["name"] != id {
		t.Errorf("%s: name %q != dir name %q", path, fm["name"], id)
	}
}

func validateTask(t *testing.T, root, id string) {
	path := filepath.Join(root, "tasks", id, "task.md")
	fm, ok := parseFrontmatter(mustRead(t, path))
	if !ok {
		t.Errorf("%s: missing front matter", path)
		return
	}
	requireKeys(t, fm, path, "kind", "id")
	if fm["kind"] != "task" {
		t.Errorf("%s: kind %q != \"task\"", path, fm["kind"])
	}
	if fm["id"] != id {
		t.Errorf("%s: id %q != dir name %q", path, fm["id"], id)
	}
}

func TestAgentsStructure(t *testing.T) {
	root := agentsDir(t)
	for _, sub := range []string{"agents", "tasks"} {
		if info, err := os.Stat(filepath.Join(root, sub)); err != nil || !info.IsDir() {
			t.Fatalf(".agents/%s directory missing", sub)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "agents.md")); err != nil {
		t.Fatalf(".agents/agents.md missing: %v", err)
	}
	agents := dirNames(t, filepath.Join(root, "agents"))
	if len(agents) == 0 {
		t.Error("no agent profiles found under .agents/agents/")
	}
	for _, id := range agents {
		validateAgent(t, root, id)
	}
	for _, id := range dirNames(t, filepath.Join(root, "tasks")) {
		validateTask(t, root, id)
	}
}

func TestTaskAgentLinkage(t *testing.T) {
	root := agentsDir(t)
	agentIDs := map[string]bool{}
	for _, name := range dirNames(t, filepath.Join(root, "agents")) {
		agentIDs[name] = true
	}
	for _, id := range dirNames(t, filepath.Join(root, "tasks")) {
		path := filepath.Join(root, "tasks", id, "task.md")
		fm, _ := parseFrontmatter(mustRead(t, path))
		if agent, ok := fm["agent"]; ok && agent != "" && !agentIDs[agent] {
			t.Errorf("%s: agent %q does not match any agent id under .agents/agents/", path, agent)
		}
	}
}

func pkgPartition() map[string][]string {
	return map[string][]string{
		"pkg-ssh-git": {
			"ssh", "gitsshsigning", "gitcredentials", "credentials",
			"pty", "shell", "dotfiles", "gpg",
		},
		"pkg-container": {
			"docker", "dockerinstall", "dockercredentials", "dockerfile",
			"compose", "image", "extract", "flatpak", "driver",
		},
		"pkg-tunnel-network": {"tunnel", "http", "netstat", "port", "inject"},
		"pkg-core-agent": {
			"agent", "client", "command", "options", "config",
			"devsyconfig", "task", "workspace", "template", "provider",
		},
		"pkg-system-platform": {
			"platform", "machineid", "apple", "selfupdate", "version",
			"status", "snapshot", "daemon", "sharedfile", "open", "scanner",
			"language", "log", "output", "stdio", "terminal", "theme", "table",
			"survey", "secrets", "token", "telemetry", "exitcode", "flags",
			"hash", "id", "random", "util", "types", "ts", "envfile",
			"encoding", "compress", "copy", "file", "download", "clierr",
			"clihelp", "git", "ide", "devcontainer",
		},
	}
}

func flattenPartition(t *testing.T, partition map[string][]string) map[string]string {
	declared := map[string]string{}
	for agent, pkgs := range partition {
		for _, p := range pkgs {
			if prev, ok := declared[p]; ok {
				t.Errorf("package %q claimed by both %s and %s", p, prev, agent)
			}
			declared[p] = agent
		}
	}
	return declared
}

func pkgAgentIDs(t *testing.T, root string) map[string]bool {
	m := map[string]bool{}
	for _, name := range dirNames(t, filepath.Join(root, "agents")) {
		if strings.HasPrefix(name, "pkg-") {
			m[name] = true
		}
	}
	return m
}

func partitionKeys(partition map[string][]string) map[string]bool {
	m := map[string]bool{}
	for k := range partition {
		m[k] = true
	}
	return m
}

func TestPackageScopePartition(t *testing.T) {
	root := agentsDir(t)
	partition := pkgPartition()
	declared := flattenPartition(t, partition)
	actual := dirNames(t, filepath.Join(root, "..", "pkg"))
	for _, p := range actual {
		if _, ok := declared[p]; !ok {
			t.Errorf("pkg/%s has no pkg-* agent; update .agents/agents.md and the partition", p)
		}
	}
	for p := range declared {
		if !slices.Contains(actual, p) {
			t.Errorf("partition declares pkg/%s but it does not exist", p)
		}
	}
	if !reflect.DeepEqual(partitionKeys(partition), pkgAgentIDs(t, root)) {
		t.Error("partition agent ids do not match .agents/agents/pkg-* directories")
	}
}

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type lintPatchFixture struct {
	repo, temp, event           string
	base, head, merge, ancestor string
}

func newLintPatchFixture(t *testing.T) lintPatchFixture {
	t.Helper()
	repo := t.TempDir()
	gitFixture(t, repo, "init", "--initial-branch=main")
	gitFixture(t, repo, "config", "user.name", "Test User")
	gitFixture(t, repo, "config", "user.email", "test@example.invalid")
	gitFixture(t, repo, "config", "commit.gpgsign", "false")
	var initial strings.Builder
	for i := range 100 {
		fmt.Fprintf(&initial, "original line %03d\n", i)
	}
	writeFixture(t, filepath.Join(repo, "shared.txt"), initial.String())
	gitFixture(t, repo, "add", "shared.txt")
	gitFixture(t, repo, "commit", "-m", "initial")
	ancestor := gitFixture(t, repo, "rev-parse", "HEAD")
	gitFixture(t, repo, "checkout", "-b", "feature")
	headContent := strings.Replace(
		initial.String(),
		"original line 099",
		"head-only final change",
		1,
	)
	writeFixture(t, filepath.Join(repo, "shared.txt"), headContent)
	var large strings.Builder
	for i := range 21050 {
		fmt.Fprintf(&large, "feature patch line %05d\n", i)
	}
	writeFixture(t, filepath.Join(repo, "large.txt"), large.String())
	writeFixture(t, filepath.Join(repo, "z-last.txt"), "late-head-marker\n")
	gitFixture(t, repo, "add", "shared.txt", "large.txt", "z-last.txt")
	gitFixture(t, repo, "commit", "-m", "feature")
	head := gitFixture(t, repo, "rev-parse", "HEAD")
	gitFixture(t, repo, "checkout", "main")
	writeFixture(t, filepath.Join(repo, "shared.txt"), "base-only line\n"+initial.String())
	gitFixture(t, repo, "add", "shared.txt")
	gitFixture(t, repo, "commit", "-m", "base")
	base := gitFixture(t, repo, "rev-parse", "HEAD")
	gitFixture(t, repo, "merge", "--no-ff", "--no-edit", head)
	merge := gitFixture(t, repo, "rev-parse", "HEAD")
	gitFixture(t, repo, "checkout", "--detach", head)
	temp := t.TempDir()
	event := filepath.Join(t.TempDir(), "event.json")
	writeLintEvent(t, event, base, head)
	return lintPatchFixture{
		repo:     repo,
		temp:     temp,
		event:    event,
		base:     base,
		head:     head,
		merge:    merge,
		ancestor: ancestor,
	}
}

func gitFixture(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLintEvent(t *testing.T, path, base, head string) {
	t.Helper()
	event := map[string]any{"pull_request": map[string]any{
		"base": map[string]string{"sha": base}, "head": map[string]string{"sha": head},
	}}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data))
}

func (f lintPatchFixture) input() lintPatchInput {
	return lintPatchInput{repo: f.repo, eventPath: f.event, runnerTemp: f.temp}
}

func TestLintPatchIncludesCompleteHeadDiff(t *testing.T) {
	f := newLintPatchFixture(t)
	result, err := generateLintPatch(f.input())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.path)
	if err != nil {
		t.Fatal(err)
	}
	assertLintPatchContents(t, data, result, f)
	assertHeadAndMergedFixture(t, f)
}

func assertHeadAndMergedFixture(t *testing.T, f lintPatchFixture) {
	t.Helper()
	head := gitFixture(t, f.repo, "show", "HEAD:shared.txt")
	if strings.HasPrefix(head, "base-only line\n") ||
		!strings.HasSuffix(head, "head-only final change") {
		t.Fatal("head checkout does not preserve the head-side source")
	}
	merged := gitFixture(t, f.repo, "show", f.merge+":shared.txt")
	if !strings.HasPrefix(merged, "base-only line\n") ||
		!strings.HasSuffix(merged, "head-only final change") {
		t.Fatal("fixture merge did not retain both base and head changes")
	}
}

func assertLintPatchContents(
	t *testing.T,
	data []byte,
	result lintPatchResult,
	f lintPatchFixture,
) {
	t.Helper()
	patch := string(data)
	if strings.Count(patch, "\n") <= 20000 || !strings.Contains(patch, "+late-head-marker\n") {
		t.Fatal("patch omitted its large body or final file")
	}
	if !strings.Contains(patch, "+head-only final change\n") ||
		strings.Contains(patch, "base-only line") {
		t.Fatal("patch was not computed from merge base to pull request head")
	}
	if !strings.Contains(patch, "@@ -97,4 +97,4 @@") {
		t.Fatal("patch did not retain the head-side hunk position")
	}
	assertLintPatchBinding(t, data, result, f)
}

func assertLintPatchBinding(t *testing.T, data []byte, result lintPatchResult, f lintPatchFixture) {
	t.Helper()
	if result.base != f.base || result.head != f.head || result.checkout != f.head ||
		result.mergeBase != f.ancestor {
		t.Fatalf("unexpected commit binding: %+v", result)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != result.digest {
		t.Fatalf("patch digest = %s, want %s", result.digest, got)
	}
	assertLintPatchFile(t, result.path, f.temp)
}

func assertLintPatchFile(t *testing.T, path, temp string) {
	t.Helper()
	assertLintPatchContainment(t, path, temp)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("patch is not a regular file: %v %v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("patch permissions: %v", info.Mode())
	}
}

func assertLintPatchContainment(t *testing.T, path, temp string) {
	t.Helper()
	rel, err := filepath.Rel(temp, path)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatalf("patch is outside its temporary directory: %q %v", rel, err)
	}
}

func TestLintPatchSafePaths(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{"/tmp/runner-temp", true},
		{"C:/runner-temp", true},
		{"C:/", true},
		{"relative/path", false},
		{"//server/share", false},
		{"//?/C:/temp", false},
		{"C:/temp/D:/file", false},
		{"C:/temp with spaces", false},
		{"/tmp/file;unsafe", false},
	} {
		if got := lintSafePath.MatchString(test.path); got != test.want {
			t.Errorf("safe path %q = %t, want %t", test.path, got, test.want)
		}
	}
	for _, path := range []string{
		"relative/path",
		filepath.Join(os.TempDir(), "safe") + string(os.PathSeparator) + "..",
		"//server/share",
	} {
		file, _, err := openLintPatch(path)
		if err == nil {
			_ = file.Close()
			t.Errorf("accepted unsafe path %q", path)
		}
	}
}

func TestLintPatchRejectsInvalidEventsAndCheckouts(t *testing.T) {
	f := newLintPatchFixture(t)
	for _, test := range []struct {
		name string
		edit func(*lintPatchInput)
	}{
		{"missing event", func(input *lintPatchInput) { input.eventPath = "" }},
		{"missing file", func(input *lintPatchInput) { input.eventPath = filepath.Join(f.temp, "missing") }},
		{"unsafe temporary path", func(input *lintPatchInput) { input.runnerTemp += ";unsafe" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := f.input()
			test.edit(&input)
			if _, err := generateLintPatch(input); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	for _, test := range []struct{ name, base, head string }{
		{"missing base", "", f.head},
		{"malicious base", "--upload-pack=unsafe", f.head},
		{"abbreviated head", f.base, f.head[:12]},
		{"mismatched head", f.base, f.ancestor},
		{"missing object", strings.Repeat("a", 40), f.head},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := filepath.Join(t.TempDir(), "event.json")
			writeLintEvent(t, event, test.base, test.head)
			input := f.input()
			input.eventPath = event
			if _, err := generateLintPatch(input); err == nil {
				t.Fatal("accepted invalid event")
			}
		})
	}
	for _, checkout := range []struct{ name, commit string }{
		{"synthetic merge checkout", f.merge},
		{"base checkout", f.base},
	} {
		t.Run(checkout.name, func(t *testing.T) {
			gitFixture(t, f.repo, "checkout", "--detach", checkout.commit)
			if _, err := generateLintPatch(f.input()); err == nil {
				t.Fatal("accepted checkout other than pull request head")
			}
		})
	}
}

func TestLintPatchRejectsEmptyDiff(t *testing.T) {
	f := newLintPatchFixture(t)
	if _, _, err := writeLintPatch(f.repo, f.temp, f.ancestor, f.ancestor); err == nil {
		t.Fatal("accepted empty patch")
	}
}

func TestLintPatchRejectsMalformedEvent(t *testing.T) {
	for _, body := range []string{"{", "{}", "{} {}", setupNullDoc, "[]"} {
		path := filepath.Join(t.TempDir(), "event.json")
		writeFixture(t, path, body)
		if _, _, err := readLintPatchEvent(path); err == nil {
			t.Fatalf("accepted malformed event %q", body)
		}
	}
	path := filepath.Join(t.TempDir(), "event.json")
	writeLintEvent(t, path, strings.Repeat("a", 40), strings.Repeat("b", 40))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data)+" {}")
	if _, _, err := readLintPatchEvent(path); err == nil {
		t.Fatal("accepted trailing event data")
	}
}

func TestLintPatchRejectsGitErrorsAndAmbiguousBases(t *testing.T) {
	f := newLintPatchFixture(t)
	if _, _, err := writeLintPatch(f.repo, f.temp, strings.Repeat("a", 40), f.head); err == nil {
		t.Fatal("accepted git diff error")
	}
	tree := gitFixture(t, f.repo, "rev-parse", f.ancestor+"^{tree}")
	a := f.treeCommit(t, tree, "branch a", f.ancestor)
	b := f.treeCommit(t, tree, "branch b", f.ancestor)
	left := f.treeCommit(t, tree, "left merge", a, b)
	right := f.treeCommit(t, tree, "right merge", b, a)
	if _, err := uniqueLintMergeBase(f.repo, left, right); err == nil {
		t.Fatal("accepted multiple merge bases")
	}
	unrelated := f.treeCommit(t, tree, "unrelated")
	if _, err := uniqueLintMergeBase(f.repo, left, unrelated); err == nil {
		t.Fatal("accepted unrelated histories")
	}
}

func (f lintPatchFixture) treeCommit(t *testing.T, tree, message string, parents ...string) string {
	t.Helper()
	args := []string{"commit-tree", tree}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = f.repo
	cmd.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Stdin = strings.NewReader(message + "\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit-tree: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

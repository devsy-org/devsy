package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/command"
	"gotest.tools/assert"
)

func TestLFSModeResolution(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want LFSMode
	}{
		{"default is full", nil, LFSFull},
		{"explicit full", []Option{WithLFSMode(LFSFull)}, LFSFull},
		{"setup only", []Option{WithLFSMode(LFSSetupOnly)}, LFSSetupOnly},
		{"skip", []Option{WithLFSMode(LFSSkip)}, LFSSkip},
		{"WithSkipLFS maps to skip", []Option{WithSkipLFS()}, LFSSkip},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newCloneConfig(tc.opts...).lfsMode; got != tc.want {
				t.Errorf("lfsMode = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSmudgeAlwaysSkippedForClone(t *testing.T) {
	// The smudge filter is skipped at clone time for every mode; hydration is
	// done explicitly afterward (or not at all).
	for _, mode := range []LFSMode{LFSFull, LFSSetupOnly, LFSSkip} {
		if !smudgeSkippedForClone(mode) {
			t.Errorf("smudgeSkippedForClone(%v) = false, want true", mode)
		}
	}
}

// lfsSubcommands returns the `lfs ...` invocations a fakeRunner recorded.
func lfsSubcommands(fake *fakeRunner) [][]string {
	var out [][]string
	for _, c := range fake.calls {
		if len(c.Args) > 0 && c.Args[0] == "lfs" {
			out = append(out, c.Args)
		}
	}
	return out
}

// newLFSRepo returns a temp worktree that declares LFS (so SetupLFS proceeds
// past detection) and a fakeRunner capturing its git invocations.
func newLFSRepo(t *testing.T) (string, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, gitAttributesFile),
		[]byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, &fakeRunner{}
}

func TestSetupLFSModeCommands(t *testing.T) {
	if !command.Exists(binGitLFS) {
		t.Skip("git-lfs not installed")
	}

	cases := []struct {
		name string
		mode LFSMode
		want []string // expected joined `lfs ...` subcommands
	}{
		{"full installs and pulls", LFSFull, []string{"lfs install --local", "lfs pull"}},
		{"setup-only installs, no pull", LFSSetupOnly, []string{"lfs install --local"}},
		{"skip does nothing", LFSSkip, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, fake := newLFSRepo(t)
			At(dir, WithRunner(fake)).SetupLFS(context.Background(), tc.mode)

			var got []string
			for _, args := range lfsSubcommands(fake) {
				got = append(got, strings.Join(args, " "))
			}
			assert.DeepEqual(t, tc.want, got)
		})
	}
}

func TestRepoUsesLFS(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		expected bool
	}{
		{
			name:     "no gitattributes",
			files:    map[string]string{"README.md": "hi"},
			expected: false,
		},
		{
			name:     "gitattributes without lfs",
			files:    map[string]string{gitAttributesFile: "*.txt text\n"},
			expected: false,
		},
		{
			name: "root gitattributes with lfs",
			files: map[string]string{
				gitAttributesFile: "*.bin filter=lfs diff=lfs merge=lfs -text\n",
			},
			expected: true,
		},
		{
			name: "nested gitattributes with lfs",
			files: map[string]string{
				"services/a/.gitattributes": "data/x.jsonl filter=lfs diff=lfs merge=lfs -text\n",
			},
			expected: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.files {
				p := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := repoUsesLFS(dir); got != tc.expected {
				t.Errorf("repoUsesLFS = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestRepoUsesLFSSkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// A .gitattributes inside .git must not trigger detection.
	if err := os.WriteFile(
		filepath.Join(gitDir, gitAttributesFile),
		[]byte("x filter=lfs\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if repoUsesLFS(dir) {
		t.Error("repoUsesLFS should ignore .git contents")
	}
}

func TestRepoUsesLFSRespectsMaxDepth(t *testing.T) {
	// A .gitattributes at exactly the max depth is found; one level deeper is not.
	atLimit := strings.Repeat("d/", lfsDetectMaxDepth)   // lfsDetectMaxDepth dirs deep
	tooDeep := strings.Repeat("d/", lfsDetectMaxDepth+1) // one level beyond the bound
	content := "x filter=lfs diff=lfs merge=lfs -text\n"

	cases := []struct {
		name     string
		rel      string
		expected bool
	}{
		{"at max depth", filepath.Join(filepath.FromSlash(atLimit), gitAttributesFile), true},
		{"beyond max depth", filepath.Join(filepath.FromSlash(tooDeep), gitAttributesFile), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, tc.rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := repoUsesLFS(dir); got != tc.expected {
				t.Errorf("repoUsesLFS = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestDirDepth(t *testing.T) {
	root := filepath.FromSlash("/tmp/repo")
	cases := map[string]int{
		"/tmp/repo":         0,
		"/tmp/repo/a":       1,
		"/tmp/repo/a/b":     2,
		"/tmp/repo/a/b/c/d": 4,
	}
	for dir, want := range cases {
		if got := dirDepth(root, filepath.FromSlash(dir)); got != want {
			t.Errorf("dirDepth(%q) = %d, want %d", dir, got, want)
		}
	}
}

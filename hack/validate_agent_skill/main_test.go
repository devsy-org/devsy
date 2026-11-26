package main

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testPython = "python-under-test"
	cliRef     = "references/cli.md"
	safetyRef  = "references/safety.md"
	skillMD    = "SKILL.md"
)

var errBoom = errors.New("boom")

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillFixture returns a repository root holding a complete source skill.
func skillFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range requiredSkillFiles {
		writeFile(t, filepath.Join(skillDirFor(root), filepath.FromSlash(rel)), "content of "+rel)
	}
	return root
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fakeRunner records every command. failAt is the index of the call that
// returns errBoom (-1 for none). copyInstalls makes the fake npx install the
// source skill into its working directory, as the real installer does.
type fakeRunner struct {
	t            *testing.T
	repoRoot     string
	failAt       int
	copyInstalls bool
	calls        []command
}

func (f *fakeRunner) run(c command) error {
	idx := len(f.calls)
	f.calls = append(f.calls, c)
	if idx == f.failAt {
		return errBoom
	}
	if c.name == "npx" && f.copyInstalls {
		agent := filepath.Base(c.dir)
		for _, in := range agentInstalls {
			if in.agent == agent {
				copyTree(f.t, skillDirFor(f.repoRoot), filepath.Join(c.dir, in.dir))
			}
		}
	}
	return nil
}

func validationFor(root string, f *fakeRunner, out *bytes.Buffer) validation {
	return validation{repoRoot: root, python: testPython, goos: "linux", run: f.run, stdout: out}
}

func TestCheckSourceFiles(t *testing.T) {
	t.Run("complete skill", func(t *testing.T) {
		if err := checkSourceFiles(skillDirFor(skillFixture(t))); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing reference", func(t *testing.T) {
		root := skillFixture(t)
		safety := filepath.Join(skillDirFor(root), filepath.FromSlash(safetyRef))
		if err := os.Remove(safety); err != nil {
			t.Fatal(err)
		}
		err := checkSourceFiles(skillDirFor(root))
		if err == nil || err.Error() != "Missing skill file: "+safetyRef {
			t.Fatalf("got %v, want missing %s", err, safetyRef)
		}
	})

	t.Run("empty SKILL.md", func(t *testing.T) {
		root := skillFixture(t)
		writeFile(t, filepath.Join(skillDirFor(root), skillMD), "")
		err := checkSourceFiles(skillDirFor(root))
		if err == nil || err.Error() != "Missing skill file: "+skillMD {
			t.Fatalf("got %v, want missing %s", err, skillMD)
		}
	})
}

func TestVenvExecutable(t *testing.T) {
	venv := filepath.Join("tmp", "validator")
	tests := []struct {
		goos, name, want string
	}{
		{"linux", "python", filepath.Join(venv, "bin", "python")},
		{"darwin", "skills-ref", filepath.Join(venv, "bin", "skills-ref")},
		{"windows", "python", filepath.Join(venv, "Scripts", "python.exe")},
		{"windows", "skills-ref", filepath.Join(venv, "Scripts", "skills-ref.exe")},
	}
	for _, tt := range tests {
		if got := venvExecutable(venv, tt.goos, tt.name); got != tt.want {
			t.Errorf("venvExecutable(%s, %s) = %q, want %q", tt.goos, tt.name, got, tt.want)
		}
	}
}

func baseTree() map[string]string {
	return map[string]string{
		skillMD:   "skill",
		cliRef:    "cli",
		safetyRef: "safety",
	}
}

// tree returns baseTree with overrides applied and drop removed.
func tree(overrides map[string]string, drop string) map[string]string {
	out := baseTree()
	maps.Copy(out, overrides)
	delete(out, drop)
	return out
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

func assertErrContains(t *testing.T, err error, subs []string) {
	t.Helper()
	if len(subs) == 0 {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, sub := range subs {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not contain %q", err, sub)
		}
	}
}

func TestCompareTrees(t *testing.T) {
	tests := []struct {
		name    string
		got     map[string]string
		wantErr []string // substrings the error must contain; nil means no error
	}{
		{name: "equal", got: tree(nil, "")},
		{
			name:    "changed bytes",
			got:     tree(map[string]string{cliRef: "changed"}, ""),
			wantErr: []string{cliRef, "content differs"},
		},
		{
			name:    "missing file",
			got:     tree(nil, safetyRef),
			wantErr: []string{"missing", safetyRef},
		},
		{
			name:    "extra file",
			got:     tree(map[string]string{"references/extra.md": "x"}, ""),
			wantErr: []string{"extra", "references/extra.md"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compareTrees(writeTree(t, baseTree()), writeTree(t, tt.got))
			assertErrContains(t, err, tt.wantErr)
		})
	}
}

// TestValidateStopsAtFirstFailure fails each step in turn and requires the
// error to surface and no later step to run. Call order: python version
// check, venv, pip install, skills-ref validate, codex install, claude install.
func TestValidateStopsAtFirstFailure(t *testing.T) {
	steps := []struct {
		name string
		want string
	}{
		{"python version check", pythonRequired},
		{"venv creation", ""},
		{"pip install", ""},
		{"skills-ref validate", ""},
		{"codex install", "codex install"},
		{"claude-code install", "claude-code install"},
	}
	for i, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			root := skillFixture(t)
			f := &fakeRunner{t: t, repoRoot: root, failAt: i, copyInstalls: true}
			var out bytes.Buffer
			err := validate(validationFor(root, f, &out))
			if !errors.Is(err, errBoom) {
				t.Fatalf("got %v, want wrapped errBoom", err)
			}
			if step.want != "" && !strings.Contains(err.Error(), step.want) {
				t.Errorf("error %q does not contain %q", err, step.want)
			}
			if len(f.calls) != i+1 {
				t.Errorf("ran %d commands, want %d (stop at failing step)", len(f.calls), i+1)
			}
			if out.Len() != 0 {
				t.Errorf("success output written after failure: %q", out.String())
			}
		})
	}
}

func TestValidateInstalledSkillMissing(t *testing.T) {
	root := skillFixture(t)
	f := &fakeRunner{t: t, repoRoot: root, failAt: -1, copyInstalls: false}
	var out bytes.Buffer
	err := validate(validationFor(root, f, &out))
	if err == nil || !strings.Contains(err.Error(), "SKILL.md missing or empty") {
		t.Fatalf("got %v, want installed SKILL.md failure", err)
	}
	if out.Len() != 0 {
		t.Errorf("success output written after failure: %q", out.String())
	}
}

func TestValidateSucceeds(t *testing.T) {
	root := skillFixture(t)
	f := &fakeRunner{t: t, repoRoot: root, failAt: -1, copyInstalls: true}
	var out bytes.Buffer
	if err := validate(validationFor(root, f, &out)); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := out.String(); got != successMessage+"\n" {
		t.Errorf("stdout = %q, want %q", got, successMessage+"\n")
	}
}

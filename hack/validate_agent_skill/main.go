// Command validate_agent_skill checks the end-user Devsy agent skill.
//
// It runs the upstream skills-ref validator against the source skill, then
// installs the skill with the Vercel skills CLI for Codex and Claude Code and
// requires each installed copy to match the source byte for byte.
//
// Run from the repository root, or pass --repo-root:
//
//	go run ./hack/validate_agent_skill
//
// Requires npx (Node.js), Python 3.11+ (PYTHON overrides python3), and network
// access to fetch the pinned validator and installer.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
)

const (
	skillsVersion   = "1.7.0"
	skillsRefCommit = "69ef37e9424c0a7ea9dd2293b559e43ec8176379"
	skillsRefSpec   = "skills-ref @ git+https://github.com/agentskills/agentskills.git@" +
		skillsRefCommit + "#subdirectory=skills-ref"

	pythonVersionCheck = "import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)"
	pythonRequired     = "Python 3.11+ is required (set PYTHON to its executable)"
	successMessage     = "Devsy skill conformance, discovery, and Codex/Claude Code installation passed."
)

var requiredSkillFiles = []string{
	"SKILL.md",
	"references/mcp-tools.md",
	"references/cli.md",
	"references/workspace-workflows.md",
	"references/providers.md",
	"references/safety.md",
	"references/troubleshooting.md",
}

type agentInstall struct {
	agent string
	dir   string // installed skill directory under the agent's temp install root
}

var agentInstalls = []agentInstall{
	{agent: "codex", dir: filepath.Join(".agents", "skills", "devsy")},
	{agent: "claude-code", dir: filepath.Join(".claude", "skills", "devsy")},
}

// command is one external process invocation.
type command struct {
	name   string
	args   []string
	dir    string
	env    []string // nil inherits the parent environment
	stdout io.Writer
}

// runner executes a command. execCommand is the only real implementation.
type runner func(c command) error

func execCommand(c command) error {
	cmd := exec.Command(c.name, c.args...)
	cmd.Dir = c.dir
	cmd.Env = c.env
	cmd.Stdout = c.stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type validation struct {
	repoRoot string
	python   string
	goos     string // selects the virtualenv layout
	run      runner
	stdout   io.Writer
}

// missingSkillFile reports a required source skill file that is absent or empty.
type missingSkillFile string

func (m missingSkillFile) Error() string {
	return "Missing skill file: " + string(m)
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, execCommand))
}

func runCLI(args []string, stdout, stderr io.Writer, run runner) int {
	flags := flag.NewFlagSet("validate_agent_skill", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repoRoot := flags.String("repo-root", ".", "repository root containing .agents/skills/devsy")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	err = validate(validation{
		repoRoot: root,
		python:   pythonBin(),
		goos:     runtime.GOOS,
		run:      run,
		stdout:   stdout,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func pythonBin() string {
	if p := os.Getenv("PYTHON"); p != "" {
		return p
	}
	return "python3"
}

// validate runs the steps in order and stops at the first failure.
func validate(v validation) error {
	skillDir := skillDirFor(v.repoRoot)
	if err := checkSourceFiles(skillDir); err != nil {
		return err
	}
	if err := v.run(command{name: v.python, args: []string{"-c", pythonVersionCheck}}); err != nil {
		return fmt.Errorf("%s: %w", pythonRequired, err)
	}

	tmp, err := os.MkdirTemp("", "validate-agent-skill-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	env := append(os.Environ(), "DISABLE_TELEMETRY=1", "NO_COLOR=1")
	if err := validateWithSkillsRef(v, env, tmp, skillDir); err != nil {
		return err
	}
	for _, in := range agentInstalls {
		if err := checkInstall(v, env, tmp, in); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(v.stdout, successMessage)
	return nil
}

// validateWithSkillsRef creates a virtualenv with the pinned skills-ref
// validator and runs it against the source skill.
func validateWithSkillsRef(v validation, env []string, tmp, skillDir string) error {
	venv := filepath.Join(tmp, "validator")
	steps := []command{
		{name: v.python, args: []string{"-m", "venv", venv}},
		{
			name: venvExecutable(venv, v.goos, "python"),
			args: []string{
				"-m", "pip", "install", "--quiet", "--disable-pip-version-check", skillsRefSpec,
			},
		},
		{name: venvExecutable(venv, v.goos, "skills-ref"), args: []string{"validate", skillDir}},
	}
	for _, c := range steps {
		c.env = env
		c.stdout = v.stdout
		if err := v.run(c); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return nil
}

// checkInstall installs the skill for one agent with the pinned skills CLI and
// requires the installed copy to match the source tree.
func checkInstall(v validation, env []string, tmp string, in agentInstall) error {
	dir := filepath.Join(tmp, in.agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := v.run(command{
		name: "npx",
		args: []string{
			"--yes", "skills@" + skillsVersion, "add", v.repoRoot,
			"--skill", "devsy", "--agent", in.agent, "--copy", "--yes", "--json",
		},
		dir:    dir,
		env:    env,
		stdout: io.Discard,
	})
	if err != nil {
		return fmt.Errorf("%s install: %w", in.agent, err)
	}

	installed := filepath.Join(dir, in.dir)
	if !isNonEmptyFile(filepath.Join(installed, "SKILL.md")) {
		return fmt.Errorf("%s install: SKILL.md missing or empty in %s", in.agent, installed)
	}
	if err := compareTrees(skillDirFor(v.repoRoot), installed); err != nil {
		return fmt.Errorf("%s install: %w", in.agent, err)
	}
	return nil
}

func skillDirFor(repoRoot string) string {
	return filepath.Join(repoRoot, ".agents", "skills", "devsy")
}

// venvExecutable returns the path of an executable in a virtualenv built for
// goos. Windows virtualenvs keep executables in Scripts with an .exe suffix.
func venvExecutable(venvDir, goos, name string) string {
	if goos == "windows" {
		return filepath.Join(venvDir, "Scripts", name+".exe")
	}
	return filepath.Join(venvDir, "bin", name)
}

func checkSourceFiles(skillDir string) error {
	for _, rel := range requiredSkillFiles {
		if !isNonEmptyFile(filepath.Join(skillDir, filepath.FromSlash(rel))) {
			return missingSkillFile(rel)
		}
	}
	return nil
}

func isNonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

var errContentDiffers = errors.New("content differs")

// compareTrees reports the first difference between the regular files under
// want and got: a file missing from got, a file whose bytes differ, or a file
// present only in got. Paths are compared relative to each root.
func compareTrees(want, got string) error {
	wantFiles, err := listFiles(want)
	if err != nil {
		return err
	}
	gotFiles, err := listFiles(got)
	if err != nil {
		return err
	}
	for _, rel := range wantFiles {
		if !slices.Contains(gotFiles, rel) {
			return fmt.Errorf("installed copy missing %s", rel)
		}
		if err := sameBytes(filepath.Join(want, rel), filepath.Join(got, rel)); err != nil {
			return fmt.Errorf("installed copy %s: %w", rel, err)
		}
	}
	for _, rel := range gotFiles {
		if !slices.Contains(wantFiles, rel) {
			return fmt.Errorf("installed copy has extra file %s", rel)
		}
	}
	return nil
}

// listFiles returns the sorted slash-separated paths of regular files under root.
func listFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(files)
	return files, err
}

func sameBytes(a, b string) error {
	aData, err := os.ReadFile(a)
	if err != nil {
		return err
	}
	bData, err := os.ReadFile(b)
	if err != nil {
		return err
	}
	if !bytes.Equal(aData, bData) {
		return errContentDiffers
	}
	return nil
}

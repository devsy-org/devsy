package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	scopeTestBefore = "1111111111111111111111111111111111111111"
	scopeTestSHA    = "2222222222222222222222222222222222222222"
)

const (
	docsIndexPage = "sites/docs-devsy-sh/content/docs/index.mdx"
	installScript = "sites/docs-devsy-sh/public/install.sh"
	readmeFile    = "README.md"
)

var errScopeTest = errors.New("scope test failure")

type fakeHistory struct {
	parents  []string
	files    []string
	parentEr error
	fetchEr  error
	filesEr  error
}

func (f fakeHistory) Parents(context.Context, string) ([]string, error) {
	return f.parents, f.parentEr
}

func (f fakeHistory) Fetch(context.Context, string) error { return f.fetchEr }

func (f fakeHistory) ChangedFiles(context.Context, string, string) ([]string, error) {
	return f.files, f.filesEr
}

type scopeSuite struct {
	suite.Suite
	selection SelectionConfig
}

func TestScopeSuite(t *testing.T) {
	suite.Run(t, new(scopeSuite))
}

func (s *scopeSuite) SetupSuite() {
	manifest, err := LoadManifest()
	s.Require().NoError(err)
	s.selection = manifest.Selection
}

func singleCommit(files ...string) fakeHistory {
	return fakeHistory{parents: []string{scopeTestBefore}, files: files}
}

func (s *scopeSuite) TestChangedFiles() {
	tests := []struct {
		name  string
		files []string
		light bool
	}{
		{"docs site only", []string{docsIndexPage}, true},
		{"design only", []string{"design/tokens/tokens.css"}, true},
		{"markdown only", []string{readmeFile, "CONTRIBUTING.md"}, true},
		{"renovate config", []string{"renovate.json"}, true},
		{
			"docs mix",
			[]string{"sites/dl-devsy-sh/index.html", "SECURITY.md", "renovate.json"},
			true,
		},
		{"go file", []string{"pkg/shell/shell.go"}, false},
		{"go test under docs", []string{"docs/example/example_test.go"}, false},
		{"go file under sites", []string{"sites/tools/gen.go"}, false},
		{"docs and go", []string{readmeFile, "cmd/root.go"}, false},
		{"install script", []string{installScript}, false},
		{
			"install script with docs",
			[]string{readmeFile, installScript},
			false,
		},
		{
			"page a Go test reads",
			[]string{"sites/docs-devsy-sh/content/docs/managing-providers/microsandbox.mdx"},
			false,
		},
		{"agents guide a Go test reads", []string{"AGENTS.md"}, false},
		{"licenses file", []string{"THIRD_PARTY_LICENSES.md"}, false},
		{"markdown inside a Go tree", []string{"pkg/docker/README.md"}, false},
		{"markdown in hack", []string{"hack/ci/README.md"}, false},
		{"agent skill", []string{".agents/skills/devsy/SKILL.md"}, false},
		{"workflow", []string{".github/workflows/pr-ci.yml"}, false},
		{"mise config", []string{"mise.toml"}, false},
		{"mise lock", []string{"mise.lock"}, false},
		{"prek config", []string{"prek.toml"}, false},
		{"go mod", []string{"go.mod"}, false},
		{"desktop code", []string{"desktop/src/main/index.ts"}, false},
		{"unknown root file", []string{"Taskfile.yml"}, false},
		{
			"moved install script",
			[]string{installScript, "sites/docs-devsy-sh/public/get.sh"},
			false,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			decision := s.classify(scopeTestBefore, singleCommit(tt.files...))

			s.Equal(tt.light, decision.Light, decision.Reason)
			s.NotEmpty(decision.Reason)
		})
	}
}

func (s *scopeSuite) TestUncertainDiffRunsInFull() {
	docs := []string{readmeFile}
	tests := []struct {
		name    string
		before  string
		history fakeHistory
	}{
		{"missing previous sha", "", singleCommit(docs...)},
		{"new branch sha", scopeZeroSHA, singleCommit(docs...)},
		{
			"force push or several commits",
			scopeTestBefore,
			fakeHistory{parents: []string{"3333"}, files: docs},
		},
		{
			"merge commit",
			scopeTestBefore,
			fakeHistory{parents: []string{scopeTestBefore, "3333"}, files: docs},
		},
		{"root commit", scopeTestBefore, fakeHistory{files: docs}},
		{"cannot read commit", scopeTestBefore, fakeHistory{parentEr: errScopeTest, files: docs}},
		{
			"cannot fetch previous",
			scopeTestBefore,
			fakeHistory{parents: []string{scopeTestBefore}, fetchEr: errScopeTest, files: docs},
		},
		{
			"cannot diff",
			scopeTestBefore,
			fakeHistory{parents: []string{scopeTestBefore}, filesEr: errScopeTest, files: docs},
		},
		{"empty diff", scopeTestBefore, singleCommit()},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			decision := s.classify(tt.before, tt.history)

			s.False(decision.Light)
			s.NotEmpty(decision.Reason)
		})
	}
}

func (s *scopeSuite) TestMissingShaRunsInFull() {
	decision := classifyPush(
		context.Background(),
		singleCommit(readmeFile),
		s.selection,
		pushRange{scopeTestBefore, ""},
	)

	s.False(decision.Light)
}

func (s *scopeSuite) TestEveryFileReadByGoTestsIsAFullRunTrigger() {
	for _, file := range []string{
		installScript,
		"sites/docs-devsy-sh/content/docs/managing-providers/microsandbox.mdx",
		"AGENTS.md",
		".github/workflows/pr-ci.yml",
		"hack/ci/suites.yaml",
	} {
		s.False(isLightFile(s.selection, file), file)
	}
}

func (s *scopeSuite) TestCLIPrintsOutputsAndFailsOpenOutsideARepository() {
	var stdout, stderr bytes.Buffer

	code := scopeCLI(
		[]string{"--before", scopeTestBefore, "--sha", scopeTestSHA, "--repo-dir", s.T().TempDir()},
		&stdout, &stderr,
	)

	s.Equal(0, code)
	s.Contains(stdout.String(), "light=false\n")
	s.Contains(stdout.String(), "reason=")
}

func (s *scopeSuite) TestCLIRejectsUnknownFlag() {
	var stdout, stderr bytes.Buffer

	s.Equal(2, scopeCLI([]string{"--nope"}, &stdout, &stderr))
}

func (s *scopeSuite) TestGitHistoryParsesPushedCommit() {
	runner := &scriptedRunner{outputs: map[string]string{
		"cat-file":   "tree abc\nparent " + scopeTestBefore + "\nauthor x\n\nparent not-a-parent\n",
		"diff":       "README.md\x00sites/a.mdx\x00",
		"fetch":      "",
		"unexpected": "",
	}}
	history := gitHistory{runner: runner, dir: "."}

	parents, err := history.Parents(context.Background(), scopeTestSHA)
	s.Require().NoError(err)
	s.Equal([]string{scopeTestBefore}, parents)

	files, err := history.ChangedFiles(context.Background(), scopeTestBefore, scopeTestSHA)
	s.Require().NoError(err)
	s.Equal([]string{readmeFile, "sites/a.mdx"}, files)
	s.Contains(runner.calls[1], "--no-renames")

	s.Require().NoError(history.Fetch(context.Background(), scopeTestBefore))
	s.Equal([]string{"fetch", "--no-tags", "--depth=1", "origin", scopeTestBefore}, runner.args[2])
}

type scriptedRunner struct {
	outputs map[string]string
	calls   []string
	args    [][]string
}

func (r *scriptedRunner) Run(_ context.Context, spec commandSpec) error {
	r.args = append(r.args, spec.Args)
	r.calls = append(r.calls, strings.Join(spec.Args, " "))
	if out, ok := r.outputs[spec.Args[0]]; ok && spec.Stdout != nil {
		_, _ = spec.Stdout.Write([]byte(out))
	}
	return nil
}

// The six path outputs must all honour the scope step, and that step must fail
// open: if it errors, its light output is empty and every job still runs.
func (s *scopeSuite) TestWorkflowWiresScopeAndSchedule() {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pr-ci.yml"))
	s.Require().NoError(err)
	workflow := strings.ReplaceAll(string(raw), "\r\n", "\n")

	s.Equal(6, strings.Count(workflow, "steps.scope.outputs.light != 'true'"))
	s.Contains(workflow, "go run ./hack/ci scope")
	s.Contains(
		workflow,
		"id: scope\n        if: github.event_name == 'push' && !github.event.forced && "+
			"steps.scope_go.outcome == 'success'\n        continue-on-error: true",
	)
	s.Contains(
		workflow,
		"id: scope_go\n        if: github.event_name == 'push' && !github.event.forced\n"+
			"        continue-on-error: true",
	)
	s.Contains(workflow, "schedule:\n    - cron:")
}

func (s *scopeSuite) classify(before string, history fakeHistory) scopeDecision {
	return classifyPush(context.Background(), history, s.selection, pushRange{before, scopeTestSHA})
}

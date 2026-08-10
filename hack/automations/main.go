package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed template.md.tmpl
var templateText string

const (
	kindInstall = "install"
	kindVerify  = "verify"
	kindCommit  = "commit"
	kindPR      = "pr"
	kindNote    = "note"
)

const (
	goVersion        = "1.26.3"
	golangciVersion  = "2.12.2"
	goDownloadURL    = "https://go.dev/dl/go" + goVersion + ".linux-amd64.tar.gz"
	golangciArchive  = "golangci-lint-" + golangciVersion + "-linux-amd64.tar.gz"
	golangciDownload = "https://github.com/golangci/golangci-lint/releases/download/v" +
		golangciVersion + "/" + golangciArchive
	goTaskInstall = "timeout 600 go install github.com/go-task/task/v3/cmd/task@latest"
	goVerify      = "go version && task --version && golangci-lint --version"
	cdTidy        = "cd into the cloned devsy repo workspace and run: task cli:tidy"
	cdPlain       = "cd into the cloned devsy repo workspace"
	curlFlags     = "--retry 3 --retry-delay 5 --connect-timeout 30 --max-time 300 -sSL"
)

type agent struct {
	ID                string `yaml:"id"`
	AutomationName    string `yaml:"automation_name"`
	BranchPrefix      string `yaml:"branch_prefix"`
	CommitSubject     string `yaml:"commit_subject"`
	PRTitle           string `yaml:"pr_title"`
	JobName           string `yaml:"job_name"`
	Toolchain         string `yaml:"toolchain"`
	Intro             string `yaml:"intro"`
	PRBodyMustInclude string `yaml:"pr_body_must_include"`
	Constraints       string `yaml:"constraints"`
	Description       string `yaml:"description"`
	Scope             string `yaml:"scope"`
	SelfImprovement   string `yaml:"self_improvement"`
	Steps             []step `yaml:"steps"`
}

type step struct {
	Kind         string `yaml:"kind"`          // install, verify, commit, pr, or "" (plain)
	Title        string `yaml:"title"`         // plain-step title (full first line, incl. trailing punctuation)
	Body         string `yaml:"body"`          // plain-step body (lines after the title; optional)
	VerifyExtras string `yaml:"verify_extras"` // kind=verify: extra verify bullets
}

type config struct {
	Agents []agent `yaml:"agents"`
}

type renderView struct {
	Agent       agent
	InstallStep string
	ReviewSteps string
	CommitStep  string
	PRStep      string
}

func main() {
	check := flag.Bool("check", false, "exit 1 if any generated file differs from disk")
	sync := flag.Bool(
		"sync",
		false,
		"push generated agent.md prompts to the OpenHands automations API",
	)
	dryRun := flag.Bool("dry-run", false, "with -sync: report drift without patching")
	flag.Parse()
	cfg := loadConfig()
	for _, a := range cfg.Agents {
		validateAgent(a)
	}
	if *sync {
		runSync(cfg, *dryRun)
		return
	}
	tmpl, err := template.New("prompt").Parse(templateText)
	if err != nil {
		fail("parse template: %v", err)
	}
	dirty := false
	for _, a := range cfg.Agents {
		validateAgent(a)
		out := renderAgent(tmpl, a)
		outPath := agentPath(a.ID)
		if *check {
			if checkDrift(outPath, out) {
				dirty = true
			}
			continue
		}
		writeAgent(outPath, out)
	}
	if *check && dirty {
		os.Exit(1)
	}
}

func loadConfig() config {
	data, err := os.ReadFile("hack/automations/agents.yaml")
	if err != nil {
		fail("read agents.yaml: %v", err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fail("parse agents.yaml: %v", err)
	}
	if len(cfg.Agents) == 0 {
		fail("agents.yaml has no agents")
	}
	return cfg
}

func validateAgent(a agent) {
	if a.ID == "" || a.BranchPrefix == "" {
		fail("agent missing id or branch_prefix")
	}
	if a.AutomationName == "" {
		fail("agent %s missing automation_name", a.ID)
	}
	if a.Description == "" {
		fail("agent %s missing description", a.ID)
	}
	if a.Scope == "" {
		fail("agent %s missing scope", a.ID)
	}
	if a.SelfImprovement == "" {
		fail("agent %s missing self_improvement", a.ID)
	}
	if len(a.Steps) == 0 {
		fail("agent %s has no steps", a.ID)
	}
	kinds := map[string]bool{}
	for _, s := range a.Steps {
		kinds[s.Kind] = true
	}
	for _, required := range []string{kindInstall, kindCommit, kindPR} {
		if !kinds[required] {
			fail("agent %s steps missing a %q step", a.ID, required)
		}
	}
}

func renderAgent(tmpl *template.Template, a agent) string {
	blocks := renderSteps(a)
	view := renderView{
		Agent:       a,
		InstallStep: blocks.install,
		ReviewSteps: blocks.review,
		CommitStep:  blocks.commit,
		PRStep:      blocks.pr,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		fail("render %s: %v", a.ID, err)
	}
	return buf.String()
}

func agentPath(id string) string {
	return filepath.Join(".agents", "agents", id, "agent.md")
}

func checkDrift(path, out string) bool {
	existing, err := os.ReadFile(path)
	if err != nil {
		fail("read %s: %v", path, err)
	}
	if !bytes.Equal(existing, []byte(out)) {
		fmt.Printf("DRIFT  %s\n", path)
		return true
	}
	return false
}

func writeAgent(path, out string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
	fmt.Printf("wrote  %s\n", path)
}

func escapeJSONString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "automations: "+format+"\n", args...)
	os.Exit(1)
}

func installBlock(toolchain string) string {
	switch toolchain {
	case "go":
		return joinLines(goInstallBlock(), goVerify, cdTidy)
	case "go+node":
		return joinLines(goInstallBlock(), nodeInstallBlock(), goVerifyNode(), cdTidy)
	case "go+python":
		return joinLines(goInstallBlock(), pythonInstallBlock(), goVerifyPython(), cdTidy)
	case "go+protoc":
		return joinLines(goInstallBlock(), protocInstall(), goVerify, cdTidy)
	case "go+devcontainer":
		return joinLines(goInstallBlock(), devcontainerInstall(), devcontainerVerify(), cdTidy)
	case "minimal":
		return joinLines(cdPlain)
	case "gh+act":
		return joinLines(ghActBlock(), ghActVerify(), cdPlain)
	default:
		return toolchain
	}
}

const knownGitFailure = "KNOWN PRE-EXISTING FAILURE: pkg/git tests (TestRepoClone*) fail on " +
	"origin/main already — a stale assertion, NOT caused by your change. " +
	"If ONLY pkg/git fails and your change did not touch pkg/git, proceed. " +
	"If your change introduces any NEW lint issue or test failure, " +
	"fix the root cause or pick a different improvement."

func renderSteps(a agent) stepBlocks {
	idx := stepKindIndices(a.Steps)
	numbered := numberedStepMap(a.Steps)

	blocks := stepBlocks{
		install: renderStep(a.Steps[idx.install], numbered[idx.install], a),
		commit:  renderStep(a.Steps[idx.commit], numbered[idx.commit], a),
		pr:      renderStep(a.Steps[idx.pr], numbered[idx.pr], a),
	}
	blocks.review = joinReviewSteps(a, idx, numbered)
	return blocks
}

// stepIndices holds the list positions of the install/commit/pr steps.
type stepIndices struct {
	install int
	commit  int
	pr      int
}

// stepKindIndices returns the list positions of the install/commit/pr steps.
func stepKindIndices(steps []step) stepIndices {
	idx := stepIndices{commit: -1, pr: -1}
	for i, s := range steps {
		switch s.Kind {
		case kindInstall:
			idx.install = i
		case kindCommit:
			idx.commit = i
		case kindPR:
			idx.pr = i
		}
	}
	return idx
}

func numberedStepMap(steps []step) map[int]int {
	numbered := make(map[int]int)
	num := -1
	for i, s := range steps {
		if s.Kind == kindNote {
			continue
		}
		num++
		numbered[i] = num
	}
	return numbered
}

func joinReviewSteps(a agent, idx stepIndices, numbered map[int]int) string {
	skip := map[int]bool{idx.install: true, idx.commit: true, idx.pr: true}
	var parts []string
	for i, s := range a.Steps {
		if skip[i] {
			continue
		}
		parts = append(parts, renderStep(s, numbered[i], a))
	}
	return strings.Join(parts, "\n\n")
}

type stepBlocks struct {
	install string
	review  string
	commit  string
	pr      string
}

func renderStep(s step, num int, a agent) string {
	switch s.Kind {
	case kindInstall:
		return fmt.Sprintf("STEP %d — %s:\n    set -e\n%s", num, s.Title, installBlock(a.Toolchain))
	case kindVerify:
		return fmt.Sprintf(
			"STEP %d — Verify (CRITICAL — use CI-equivalent lint):\n%s",
			num,
			verifyBody(s),
		)
	case kindCommit:
		return fmt.Sprintf(
			"STEP %d — Commit via the Devsy GitHub App (signed, verified).\n%s",
			num,
			commitBody(a),
		)
	case kindPR:
		return fmt.Sprintf(
			"STEP %d — Open the PR as the app (no GITHUB_TOKEN):\n%s",
			num,
			prBody(a),
		)
	case kindNote:
		return s.Body
	default:
		return fmt.Sprintf("STEP %d — %s", num, s.Body)
	}
}

func verifyBody(s step) string {
	lines := []string{
		"  - mkdir -p dist",
		"  - git fetch --quiet origin main",
		"  - task cli:lint:ci      # Must be 0 new issues.",
		"  - task cli:test",
	}
	if s.VerifyExtras != "" {
		lines = append(lines, "  - "+s.VerifyExtras)
	}
	lines = append(lines, "  "+knownGitFailure)
	return joinLines(lines...)
}

// commitBody is the shared commit-instruction body (the part after the STEP line).
func commitBody(a agent) string {
	return joinLines(
		"The repo ships a Go tool (hack/sign_commit) that authenticates as the app installation",
		"and creates the commit through GitHub's GraphQL API, so GitHub signs it (committer:",
		"web-flow, verified). It auto-detects all working-tree changes (staged, unstaged, and",
		"untracked files) and auto-creates the remote branch from origin/main if needed.",
		"Do NOT run `git commit` locally — it produces an unsigned commit that fails the signature",
		"check.",
		"  - git fetch --quiet origin main",
		"  - git checkout -b "+a.BranchPrefix+"/<short-slug> origin/main",
		"  - task github:app:sign-commit -- -m "+a.CommitSubject,
		"  - Confirm output: verified=true.",
	)
}

// prBody is the shared PR-instruction body (the part after the STEP line).
func prBody(a agent) string {
	return joinLines(
		"  - Write the PR body to /tmp/pr_body.md. It MUST include: "+a.PRBodyMustInclude+
			", a line \"This PR was created by an AI agent as part of an automated daily "+a.JobName+".\"",
		"  - task github:app:sign-commit -- -pr-only -title "+escapeJSONString(a.PRTitle)+
			" -b \"$(cat /tmp/pr_body.md)\"",
		"  - Report the PR URL from the output. A run that does not produce a PR URL is a FAILED run.",
	)
}

func goInstallBlock() string {
	golangciExtract := "tar -C /tmp -xzf /tmp/gcl.tgz && sudo mv /tmp/golangci-lint-" +
		golangciVersion + "-linux-amd64/golangci-lint /usr/local/bin/ && " +
		"rm -rf /tmp/gcl.tgz /tmp/golangci-lint-" + golangciVersion + "-linux-amd64"
	return joinLines(
		"    # Check for Go toolchain — install only what is missing",
		`    export PATH="/usr/local/go/bin:$(go env GOPATH 2>/dev/null)/bin:$PATH"`,
		"    if ! command -v go >/dev/null 2>&1; then",
		`      echo "installing go `+goVersion+`"`,
		"      curl "+curlFlags+" "+goDownloadURL+" -o /tmp/go.tgz",
		"      sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz",
		`      export PATH="/usr/local/go/bin:$(go env GOPATH)/bin:$PATH"`,
		"    fi",
		"    if ! command -v task >/dev/null 2>&1; then",
		`      echo "installing task"`,
		"      "+goTaskInstall,
		"    fi",
		"    if ! command -v golangci-lint >/dev/null 2>&1; then",
		`      echo "installing golangci-lint `+golangciVersion+`"`,
		"      curl "+curlFlags+" "+golangciDownload+" -o /tmp/gcl.tgz",
		"      "+golangciExtract,
		"    fi",
	)
}

func nodeInstallBlock() string {
	return joinLines(
		"    # Check for Node — install only if missing",
		"    if ! command -v node >/dev/null 2>&1; then",
		`      echo "installing node via nodesource setup_24.x"`,
		"      curl "+curlFlags+" https://deb.nodesource.com/setup_24.x -o /tmp/ns_setup.sh",
		"      sudo -E bash /tmp/ns_setup.sh >/dev/null 2>&1 && "+
			"sudo apt-get install -y -qq nodejs >/dev/null 2>&1 || true",
		"    fi",
	)
}

func goVerifyNode() string {
	return "    go version && task --version && golangci-lint --version && node --version"
}

func pythonInstallBlock() string {
	return joinLines(
		"    # Check for uv — install only if missing (scripts declare deps inline via PEP 723)",
		"    if ! command -v uv >/dev/null 2>&1; then",
		"      curl -LsSf https://astral.sh/uv/install.sh | sh",
		"    fi",
	)
}

func goVerifyPython() string {
	return "    go version && task --version && golangci-lint --version && " +
		"uv --version && uv run hack/analytics/analyze_runs.py --sample --out-dir /tmp/analytics-verify >/dev/null"
}

func protocInstall() string {
	return joinLines(
		"    # Check for protoc — install only if missing",
		"    if ! command -v protoc >/dev/null 2>&1; then",
		"      sudo apt-get install -y -qq protobuf-compiler >/dev/null 2>&1 || true",
		"    fi",
	)
}

func devcontainerInstall() string {
	return joinLines(
		"    # Check for devcontainer CLI — install only if missing",
		"    if ! command -v devcontainer >/dev/null 2>&1; then",
		"      npm install -g @devcontainers/cli@latest 2>/dev/null || true",
		"    fi",
	)
}

func devcontainerVerify() string {
	return `    go version && task --version && golangci-lint --version && ` +
		`(devcontainer --version || echo "devcontainer cli unavailable")`
}

func ghActBlock() string {
	return joinLines(
		"    # gh CLI (for listing/viewing workflow runs) — install only if missing",
		"    if ! command -v gh >/dev/null 2>&1; then",
		`      echo "installing gh"`,
		"      curl "+curlFlags+" https://cli.github.com/packages/githubcli-archive-keyring.gpg | "+
			"sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg",
		`      echo "deb [arch=$(dpkg --print-architecture) `+
			`signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] `+
			`https://cli.github.com/packages stable main" | `+
			`sudo tee /etc/apt/sources.list.d/github-cli.list >/dev/null`,
		"      sudo apt-get update -qq && sudo apt-get install -y -qq gh",
		"    fi",
		"    # act (best-effort local workflow validation; .actrc exists). "+
			"Docker may be unavailable — that's OK.",
		"    if ! command -v act >/dev/null 2>&1; then",
		`      echo "installing act"`,
		"      curl "+curlFlags+" https://raw.githubusercontent.com/nektos/act/master/install.sh | "+
			"sudo bash -s -- -b /usr/local/bin || true",
		"    fi",
	)
}

func ghActVerify() string {
	return `    command -v gh && (command -v act || ` +
		`echo "act unavailable — will validate via YAML parse")`
}

func joinLines(lines ...string) string {
	return strings.Join(lines, "\n")
}

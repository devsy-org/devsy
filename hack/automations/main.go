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
	goVersion        = "1.26.3"
	golangciVersion  = "2.12.2"
	goDownloadURL    = "https://go.dev/dl/go" + goVersion + ".linux-amd64.tar.gz"
	golangciArchive  = "golangci-lint-" + golangciVersion + "-linux-amd64.tar.gz"
	golangciDownload = "https://github.com/golangci/golangci-lint/releases/download/v" +
		golangciVersion + "/" + golangciArchive
	goTaskInstall = "go install github.com/go-task/task/v3/cmd/task@latest"
	goVerify      = "go version && task --version && golangci-lint --version"
	cdTidy        = "cd into the cloned devsy repo workspace and run: task cli:tidy"
	cdPlain       = "cd into the cloned devsy repo workspace"
)

type agent struct {
	ID                string `yaml:"id"`
	BranchPrefix      string `yaml:"branch_prefix"`
	GitAddGlob        string `yaml:"git_add_glob"`
	CommitSubject     string `yaml:"commit_subject"`
	PRTitle           string `yaml:"pr_title"`
	JobName           string `yaml:"job_name"`
	Toolchain         string `yaml:"toolchain"`
	Step0Title        string `yaml:"step0_title"`
	CommitStepNumber  int    `yaml:"commit_step_number"`
	PRStepNumber      int    `yaml:"pr_step_number"`
	Intro             string `yaml:"intro"`
	ReviewSteps       string `yaml:"review_steps"`
	PRBodyMustInclude string `yaml:"pr_body_must_include"`
	Constraints       string `yaml:"constraints"`
	Description       string `yaml:"description"`
	Scope             string `yaml:"scope"`
	SelfImprovement   string `yaml:"self_improvement"`
}

type config struct {
	Agents []agent `yaml:"agents"`
}

type renderView struct {
	Agent            agent
	Step0Body        string
	PRTitleJSON      string
	StatusStepNumber int
}

func main() {
	check := flag.Bool("check", false, "exit 1 if any generated file differs from disk")
	flag.Parse()
	cfg := loadConfig()
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
	if a.Description == "" {
		fail("agent %s missing description", a.ID)
	}
	if a.Scope == "" {
		fail("agent %s missing scope", a.ID)
	}
	if a.SelfImprovement == "" {
		fail("agent %s missing self_improvement", a.ID)
	}
}

func renderAgent(tmpl *template.Template, a agent) string {
	view := renderView{
		Agent:            a,
		Step0Body:        installBlock(a.Toolchain),
		PRTitleJSON:      escapeJSONString(a.PRTitle),
		StatusStepNumber: a.PRStepNumber + 1,
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

func goInstallBlock() string {
	golangciExtract := "tar -C /tmp -xzf /tmp/gcl.tgz && sudo mv /tmp/golangci-lint-" +
		golangciVersion + "-linux-amd64/golangci-lint /usr/local/bin/ && " +
		"rm -rf /tmp/gcl.tgz /tmp/golangci-lint-" + golangciVersion + "-linux-amd64"
	return joinLines(
		"    # Check for Go toolchain — install only what is missing",
		`    export PATH="/usr/local/go/bin:$(go env GOPATH 2>/dev/null)/bin:$PATH"`,
		"    if ! command -v go >/dev/null 2>&1; then",
		"      curl -sSL "+goDownloadURL+" -o /tmp/go.tgz",
		"      sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz",
		`      export PATH="/usr/local/go/bin:$(go env GOPATH)/bin:$PATH"`,
		"    fi",
		"    if ! command -v task >/dev/null 2>&1; then",
		"      "+goTaskInstall,
		"    fi",
		"    if ! command -v golangci-lint >/dev/null 2>&1; then",
		"      curl -sSL "+golangciDownload+" -o /tmp/gcl.tgz",
		"      "+golangciExtract,
		"    fi",
	)
}

func nodeInstallBlock() string {
	return joinLines(
		"    # Check for Node — install only if missing",
		"    if ! command -v node >/dev/null 2>&1; then",
		"      curl -fsSL https://deb.nodesource.com/setup_24.x | "+
			"sudo -E bash - >/dev/null 2>&1 && sudo apt-get install -y -qq nodejs >/dev/null 2>&1 || true",
		"    fi",
	)
}

func goVerifyNode() string {
	return "    go version && task --version && golangci-lint --version && node --version"
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
		"      curl -sSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | "+
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
		"      curl -sSL https://raw.githubusercontent.com/nektos/act/master/install.sh | "+
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

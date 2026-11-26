//go:build linux

package ghfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"al.essio.dev/pkg/shellescape"
)

const realGH = "/usr/local/bin/gh.devsy-original"

var sshEnvironment = []string{
	"HOME",
	envGHConfig,
	envXDGConfig,
	"XDG_CACHE_HOME",
	"XDG_DATA_HOME",
	envToken,
	"GITHUB_TOKEN",
	envEnterpriseToken,
	"GITHUB_ENTERPRISE_TOKEN",
	"GH_HOST",
	envGHPath,
	"GH_PROMPT_DISABLED",
	envGHBrowser,
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"SSL_CERT_FILE",
	envSessionBus,
	envLogLevel,
}

func sshCommand(command string) []string {
	return []string{
		"-F",
		"/dev/null",
		"-T",
		"-p",
		"12050",
		"-oStrictHostKeyChecking=no",
		"-oUserKnownHostsFile=/dev/null",
		"-oLogLevel=ERROR",
		"-oBatchMode=yes",
		"-oSendEnv=" + strings.Join(sshEnvironment, " "),
		"root@127.0.0.1",
		command,
	}
}

type processResult struct {
	stdout, stderr string
	code           int
}

type processOptions struct {
	env   []string
	input string
}

func execute(
	ctx context.Context,
	options processOptions,
	binary string,
	args ...string,
) processResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	//nolint:gosec // Commands and arguments come only from the isolated fixture scenarios.
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = options.env
	cmd.Stdin = strings.NewReader(options.input)
	cmd.WaitDelay = 250 * time.Millisecond
	var stdout, stderr capturedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
	}
	if stdout.overflow || stderr.overflow {
		code = -1
	}
	return processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func overlayEnvironment(base []string, patch map[string]string) []string {
	out := make([]string, 0, len(base)+len(patch))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := patch[name]; !ok {
			out = append(out, entry)
		}
	}
	for name, value := range patch {
		out = append(out, name+"="+value)
	}
	return out
}

func loadAccounts(root string) (Accounts, error) {
	b, err := readFixture(filepath.Join(root, "accounts.json"))
	if err != nil {
		return nil, err
	}
	var accounts Accounts
	if len(b) > 65536 || json.Unmarshal(b, &accounts) != nil {
		return nil, errors.New("invalid fixture accounts")
	}
	return accounts, nil
}

func Run(ctx context.Context, root string, args []string, out io.Writer) int {
	if len(args) != 1 {
		return 2
	}
	accounts, err := loadAccounts(root)
	if err != nil {
		return 2
	}
	commands := map[string]func() int{
		"server":        func() int { return serveFixture(ctx, root, accounts, CertificatePath) },
		"server-public": func() int { return serveFixture(ctx, root, accounts, filepath.Join(root, "cert.pem")) },
		"probe":         func() int { return probeSSH(ctx, root) },
		"check":         func() int { return runFixtureChecks(ctx, root, accounts, out) },
		"after-relay":   func() int { return runRelayLossChecks(ctx, root, accounts, out) },
	}
	command := commands[args[0]]
	if command == nil {
		return 2
	}
	return command()
}

func probeSSH(ctx context.Context, root string) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result := execute(
			ctx,
			processOptions{env: os.Environ(), input: ""},
			"ssh",
			sshCommand("command -v gh")...)
		if result.code == 0 {
			if os.WriteFile(
				filepath.Join(root, "pre-install-gh"),
				[]byte(result.stdout),
				0o600,
			) == nil {
				return 0
			}
			return 1
		}
		select {
		case <-ctx.Done():
			return 1
		case <-ticker.C:
		}
	}
}

type checks struct {
	ctx            context.Context
	root           string
	accounts       Accounts
	env            []string
	report         Report
	stderr         []string
	original       string
	dispatcher     string
	registrations  string
	hostBefore     []byte
	target         string
	ready, release string
	last           processResult
}

func newChecks(ctx context.Context, root string, accounts Accounts) *checks {
	bus, _ := readFixture(filepath.Join(root, "bus"))
	env := overlayEnvironment(os.Environ(), map[string]string{
		"HTTPS_PROXY":   "http://127.0.0.1:8123",
		"HTTP_PROXY":    "http://127.0.0.1:8123",
		"NO_PROXY":      "localhost,127.0.0.1",
		"SSL_CERT_FILE": CertificatePath,
		envGHConfig: filepath.Join(
			root,
			"guest-gh",
		),
		"XDG_DATA_HOME":      filepath.Join(root, "guest-data"),
		"XDG_CACHE_HOME":     filepath.Join(root, "guest-cache"),
		"GH_PROMPT_DISABLED": "1",
		envSessionBus:        strings.TrimSpace(string(bus)),
	})
	version := execute(ctx, processOptions{env: env, input: ""}, realGH, "--version")
	return &checks{
		ctx:      ctx,
		root:     root,
		accounts: accounts,
		env:      env,
		original: filepath.Join(root, "guest-gh"),
		report: Report{
			Version:      ReportVersion,
			CLI:          strings.TrimSpace(version.stdout),
			Architecture: runtime.GOARCH,
		},
	}
}

func (c *checks) verify(name string, ok bool) bool {
	c.report.Checks = append(c.report.Checks, Check{Name: name, OK: ok})
	if !ok {
		c.report.Failure = &Failure{
			Exit: c.last.code,
			TLS: strings.Contains(c.last.stderr, "certificate") ||
				strings.Contains(c.last.stderr, "tls"),
			Lookup:       strings.Contains(c.last.stderr, "lookup"),
			Readiness:    strings.Contains(c.last.stderr, "readiness"),
			Unauthorized: strings.Contains(c.last.stderr, "401"),
			EmptyStderr:  c.last.stderr == "",
		}
	}
	return ok
}

func (c *checks) gh(input string, patch map[string]string, args ...string) processResult {
	command := shellescape.QuoteCommand(append([]string{"gh"}, args...))
	r := execute(
		c.ctx,
		processOptions{env: overlayEnvironment(c.env, patch), input: input},
		"ssh",
		sshCommand(command)...)
	c.stderr = append(c.stderr, r.stderr)
	c.last = r
	return r
}

func (c *checks) api(host, user string, patch map[string]string) bool {
	r := c.gh("", patch, commandAPI, "user", "--hostname", host, "--jq", ".login")
	return r.code == 0 && strings.TrimSpace(r.stdout) == user
}

func (c *checks) nested(input string, patch map[string]string, args ...string) processResult {
	env := overlayEnvironment(
		c.env,
		map[string]string{envPath: c.dispatcher + ":/usr/local/bin:/usr/bin:/bin"},
	)
	r := execute(
		c.ctx,
		processOptions{env: overlayEnvironment(env, patch), input: input},
		"/usr/local/bin/gh",
		args...)
	c.stderr = append(c.stderr, r.stderr)
	c.last = r
	return r
}

func (c *checks) nestedAPI(host, user string, patch map[string]string) bool {
	r := c.nested("", patch, commandAPI, "user", "--hostname", host, "--jq", ".login")
	return r.code == 0 && strings.TrimSpace(r.stdout) == user
}

func (c *checks) saved() string {
	b, _ := readFixture(filepath.Join(c.original, "hosts.yml"))
	return string(b)
}

func (c *checks) noForwardedSaved() bool {
	for _, record := range c.accounts {
		if strings.Contains(c.saved(), record.Forwarded) {
			return false
		}
	}
	return true
}

func writeFixture(path, text string) bool {
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return false
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(filepath.Base(path), []byte(text), 0o600) == nil
}

func executableFixture(path, text string) bool {
	if !writeFixture(path, text) {
		return false
	}
	// #nosec G302 -- Executable fixture shim contains no credentials.
	return os.Chmod(path, 0o700) == nil
}

type pendingCommand struct {
	cmd            *exec.Cmd
	stdout, stderr capturedOutput
	done           chan error
	joined         bool
	result         bool
	orphan         bool
}

func (c *checks) pending() *pendingCommand {
	return c.pendingAlias("pending-editor")
}

func (c *checks) pendingAlias(alias string) *pendingCommand {
	p := &pendingCommand{done: make(chan error, 1)}
	//nolint:gosec // The fixture controls the alias name; it remains a separately quoted argument.
	p.cmd = exec.CommandContext(c.ctx, "/bin/bash", "-lc", "exec gh "+shellescape.Quote(alias))
	p.cmd.Env = overlayEnvironment(
		c.env,
		map[string]string{"GH_TEST_READY": c.ready, "GH_TEST_RELEASE": c.release},
	)
	p.cmd.Stdout = &p.stdout
	p.cmd.Stderr = &p.stderr
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.cmd.WaitDelay = 250 * time.Millisecond
	if err := p.cmd.Start(); err != nil {
		p.done <- err
		return p
	}
	go func() { p.done <- p.cmd.Wait() }()
	return p
}

func (p *pendingCommand) kill(group bool) {
	if p.cmd.Process == nil {
		return
	}
	if group {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	} else {
		_ = p.cmd.Process.Kill()
	}
}

func (p *pendingCommand) join() bool {
	return p.wait(20 * time.Second)
}

func (p *pendingCommand) wait(timeout time.Duration) bool {
	if p.joined {
		return p.result
	}
	select {
	case err := <-p.done:
		p.joined = true
		p.result = err == nil
	case <-time.After(timeout):
		p.kill(true)
		select {
		case <-p.done:
			p.joined = true
		case <-time.After(time.Second):
		}
	}
	return p.result
}

func (p *pendingCommand) stop() {
	if p.joined && !p.orphan {
		return
	}
	p.kill(true)
	p.wait(2 * time.Second)
}

func (c *checks) waitReady() bool {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exists(c.ready) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func readFixture(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}

func serveFixture(ctx context.Context, root string, accounts Accounts, certificatePath string) int {
	server, err := StartAPI(root, certificatePath, "127.0.0.1:8123", accounts)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fixture API startup failed: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(root, "api-ready"), nil, 0o600); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fixture API readiness publication failed: %v\n", err)
		return 1
	}
	<-ctx.Done()
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if server.Close(cleanup) != nil {
		return 1
	}
	return 0
}

func runFixtureChecks(ctx context.Context, root string, accounts Accounts, out io.Writer) int {
	checker := newChecks(ctx, root, accounts)
	ok := checker.run()
	checker.report.Completed = ok
	if json.NewEncoder(out).Encode(checker.report) != nil {
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}

type capturedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *capturedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (1 << 20) - b.Len(); n > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, err := b.Buffer.Write(p)
	return n, err
}

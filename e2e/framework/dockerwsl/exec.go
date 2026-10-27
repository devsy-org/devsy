package dockerwsl

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

const (
	toolWSL       = "wsl.exe"
	toolSSH       = "ssh.exe"
	toolSSHKeygen = "ssh-keygen.exe"
	toolICACLS    = "icacls.exe"

	wslQuickTimeout   = 30 * time.Second
	wslScriptTimeout  = time.Minute
	wslInstallTimeout = 3 * time.Minute
	dockerCmdTimeout  = 15 * time.Second

	// Two 8 KiB daemon sections plus bounded socket, process, and listener state.
	maxDiagnosticBytes = 20 << 10
	diagnosticsFile    = "docker-wsl-diagnostics.log"
	diagnosticPerm     = stateFilePerm
)

// scriptArgPattern is the whole alphabet allowed in a script argument. Scripts
// travel as a fixed wrapper plus base64, so nothing here needs shell quoting
// through wsl.exe's command-line parsing.
var scriptArgPattern = regexp.MustCompile(`^[A-Za-z0-9_./:=@+-]+$`)

// hostPlatform is the Windows-only seam: tool discovery and detached process
// creation. Everything else goes through Dependencies.
type hostPlatform interface {
	LookPath(file string) (string, error)
	// StartDetached starts a process that must outlive this command and does
	// not wait for it.
	StartDetached(path string, args []string) error
}

// wslCall is one `wsl.exe -d <distro> -u <user> --exec argv...` invocation.
type wslCall struct {
	user    string
	argv    []string
	timeout time.Duration
}

// wslScript is a fixed shell script plus validated arguments.
type wslScript struct {
	body    string
	args    []string
	timeout time.Duration
}

// argv wraps the script into `/bin/sh -c <wrapper>`. The wrapper contains no
// double quote, so Windows argument quoting cannot corrupt it.
func (s wslScript) argv() ([]string, error) {
	for _, a := range s.args {
		if !scriptArgPattern.MatchString(a) {
			return nil, fmt.Errorf("script argument %q has characters outside the allowed set", a)
		}
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(s.body))
	wrapper := "f=$(mktemp) && printf %s " + encoded + " | base64 -d > $f && sh $f " +
		strings.Join(s.args, " ") + "; rc=$?; rm -f $f; exit $rc"
	return []string{"/bin/sh", "-c", wrapper}, nil
}

// boundedTimeout clamps a per-command timeout to the time left in the budget.
func boundedTimeout(ctx context.Context, want time.Duration) time.Duration {
	if want <= 0 {
		want = wslScriptTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		return max(min(want, time.Until(deadline)), time.Second)
	}
	return want
}

// tool resolves a Windows executable once.
func (d *dockerWSL) tool(name string) (string, error) {
	if p, ok := d.tools[name]; ok {
		return p, nil
	}
	p, err := d.host.LookPath(name)
	if err != nil {
		return "", runenv.ConfigError("%s not found on PATH: %v", name, err)
	}
	d.tools[name] = p
	return p, nil
}

// run executes cmd and maps start failure, timeout and nonzero exit to a typed
// StageError carrying a redacted, bounded output tail.
func (d *dockerWSL) run(
	ctx context.Context,
	stage string,
	code runenv.Code,
	cmd runenv.Command,
) (runenv.CommandResult, error) {
	cmd.Timeout = boundedTimeout(ctx, cmd.Timeout)
	res, err := d.deps.Run.Run(ctx, cmd)
	res.Stdout = normalizeWSLOutput(res.Stdout)
	res.Stderr = normalizeWSLOutput(res.Stderr)
	switch {
	case err != nil:
		return res, runenv.NewStageError(code, stage, err)
	case res.TimedOut:
		return res, runenv.NewStageError(code, stage, fmt.Errorf("timed out after %s", cmd.Timeout)).
			WithDetail(res.Stderr + res.Stdout)
	case res.ExitCode != 0:
		return res, runenv.NewStageError(code, stage, errors.New("command failed")).
			WithExitCode(res.ExitCode).WithDetail(res.Stderr + res.Stdout)
	}
	return res, nil
}

func (d *dockerWSL) wslArgs(user string, argv []string) []string {
	args := []string{"-d", d.state.Distribution, "-u", user, "--exec"}
	return append(args, argv...)
}

// wsl runs argv inside the distribution.
func (d *dockerWSL) wsl(
	ctx context.Context,
	stage string,
	code runenv.Code,
	call wslCall,
) (runenv.CommandResult, error) {
	path, err := d.tool(toolWSL)
	if err != nil {
		return runenv.CommandResult{}, err
	}
	cmd := runenv.Command{Path: path, Args: d.wslArgs(call.user, call.argv), Timeout: call.timeout}
	return d.run(ctx, stage, code, cmd)
}

// script runs a fixed script as root inside the distribution.
func (d *dockerWSL) script(
	ctx context.Context,
	stage string,
	code runenv.Code,
	s wslScript,
) (runenv.CommandResult, error) {
	argv, err := s.argv()
	if err != nil {
		return runenv.CommandResult{}, runenv.NewStageError(runenv.CodeConfigInvalid, stage, err)
	}
	return d.wsl(ctx, stage, code, wslCall{user: rootUser, argv: argv, timeout: s.timeout})
}

// startDetached launches `sh -c <command>` inside the distribution as a process
// that outlives this command; the Windows-side wsl.exe stays attached to it.
func (d *dockerWSL) startDetached(stage string, code runenv.Code, command string) error {
	path, err := d.tool(toolWSL)
	if err != nil {
		return err
	}
	args := d.wslArgs(rootUser, []string{"/bin/sh", "-c", command})
	if err := d.host.StartDetached(path, args); err != nil {
		return runenv.NewStageError(code, stage, fmt.Errorf("start detached process: %w", err))
	}
	return nil
}

// wslDaemonID reads the daemon ID from inside the distribution.
func (d *dockerWSL) wslDaemonID(ctx context.Context, stage string) (string, error) {
	res, err := d.wsl(ctx, stage, runenv.CodeDockerWSLDaemon, wslCall{
		user:    rootUser,
		argv:    []string{"docker", "info", "--format", "{{.ID}}"},
		timeout: dockerCmdTimeout,
	})
	if err != nil {
		return "", err
	}
	id, err := parseDaemonID(res.Stdout)
	if err != nil {
		return "", runenv.NewStageError(runenv.CodeDockerWSLDaemon, stage, err)
	}
	return id, nil
}

// dockerCommand runs the isolated Windows docker.exe with an explicit
// DOCKER_HOST and an empty DOCKER_CONTEXT, never inheriting either.
func (d *dockerWSL) dockerCommand(host string, args ...string) runenv.Command {
	return runenv.Command{
		Path: filepath.Join(d.state.Owned.DockerBinDirectory, dockerExeName),
		Args: args,
		Env: []string{
			"DOCKER_HOST=" + host,
			"DOCKER_CONTEXT=",
			"DOCKER_CONFIG=" + d.state.Owned.DockerConfigDir,
		},
		Timeout: dockerCmdTimeout,
	}
}

// listenerPresent runs the /proc/net/tcp probe: true when a listener exists.
func (d *dockerWSL) listenerPresent(ctx context.Context, stage, portHex string) (bool, error) {
	argv, err := wslScript{body: tcpListenScript, args: []string{portHex}}.argv()
	if err != nil {
		return false, err
	}
	path, err := d.tool(toolWSL)
	if err != nil {
		return false, err
	}
	cmd := runenv.Command{Path: path, Args: d.wslArgs(rootUser, argv), Timeout: wslQuickTimeout}
	cmd.Timeout = boundedTimeout(ctx, cmd.Timeout)
	res, err := d.deps.Run.Run(ctx, cmd)
	switch {
	case err != nil:
		return false, runenv.NewStageError(runenv.CodeDockerWSLDaemon, stage, err)
	case res.TimedOut:
		return false, runenv.NewStageError(
			runenv.CodeDockerWSLDaemon,
			stage,
			errors.New("listener probe timed out"),
		)
	case res.ExitCode == 0:
		return true, nil
	case res.ExitCode == 1:
		return false, nil
	}
	return false, runenv.NewStageError(runenv.CodeDockerWSLDaemon, stage,
		fmt.Errorf("cannot read /proc/net/tcp (exit %d)", res.ExitCode))
}

// requireNoDockerTCP fails when an unencrypted Docker API listener exists.
func (d *dockerWSL) requireNoDockerTCP(ctx context.Context, stage string) error {
	listening, err := d.listenerPresent(ctx, stage, dockerTCPPortHex)
	if err != nil {
		return err
	}
	if listening {
		return runenv.NewStageError(runenv.CodeDockerWSLOracle, stage,
			errors.New("a Docker API is listening on unencrypted TCP port 2375"))
	}
	return nil
}

// collectDiagnostics appends bounded, redacted WSL log tails to the diagnostics
// file beside the state file and returns its path ("" when nothing was saved).
// It is best effort: a failure here never masks the error being diagnosed.
func (d *dockerWSL) collectDiagnostics(ctx context.Context, phase string) string {
	if !d.hasLinuxResources() {
		return ""
	}
	res, err := d.script(ctx, "diagnostics", runenv.CodeDockerWSLCleanup,
		wslScript{body: diagnosticsScript, timeout: wslQuickTimeout})
	text := res.Stdout + res.Stderr
	if err != nil {
		text += "\ndiagnostics command failed: " + err.Error()
	}
	path := filepath.Join(d.guard.stateDir, diagnosticsFile)
	body := fmt.Sprintf("### %s run=%s\n%s\n", phase, d.state.RunID,
		runenv.TailBytes(runenv.Redact(strings.TrimSpace(text)), maxDiagnosticBytes))
	if err := d.deps.Files.AppendFile(path, []byte(body), diagnosticPerm); err != nil {
		_, _ = fmt.Fprintf(d.deps.Err, "warning: could not save diagnostics: %v\n", err)
		return ""
	}
	return path
}

func (d *dockerWSL) hasLinuxResources() bool {
	o := d.state.Owned
	return o.DockerPidfile != "" || o.SSHDPidfile != "" || o.CIUser != "" || o.SSHDConfigDir != ""
}

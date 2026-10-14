package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	profileLegacy              = "legacy"
	profileLinuxPodmanRootless = "linux-podman-rootless"
	profileDockerWSLSSH        = "docker-wsl-ssh"
	// profileStandard is reserved for a future, explicitly selected profile.
	profileStandard = "standard"

	podmanModeNone     = "none"
	podmanModeTrue     = "true"
	podmanModeRootful  = "rootful"
	podmanModeRootless = "rootless"

	goosLinux   = "linux"
	goosWindows = "windows"

	// legacyWindowsDockerHost is the existing Podman machine pipe of Windows rows.
	legacyWindowsDockerHost = "npipe:////./pipe/podman-machine-default"
	// deadDockerHost is a pipe with no daemon: the provider endpoint must be
	// used, never the process default (issue #1469).
	deadDockerHost = "npipe:////./pipe/devsy-ci-no-daemon"

	defaultE2ETimeout = 1500 * time.Second
	// suiteKillGrace lets the test binary's own -test.timeout fire first.
	suiteKillGrace = 30 * time.Second
	preflightLimit = 2 * time.Minute
)

// dockerWSLRequiredSpecs is the identity contract of the docker-wsl-ssh
// profile: each title must execute exactly once and pass. Additional specs
// under the label are allowed.
var dockerWSLRequiredSpecs = []string{
	"dials the provider's daemon through docker system dial-stdio",
	"loads an image built by raw BuildKit into the provider's daemon",
	"BuildDevContainer builds on the provider's SSH daemon despite a dead process DOCKER_HOST",
	"BuildDevContainer fails instead of falling back to the process daemon",
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// dockerWSLPreflight verifies that docker-wsl setup completed, using the state
// file at stateFile, before the suite starts. The docker-wsl implementation
// replaces it from an init function; until then the profile fails closed.
var dockerWSLPreflight = func(context.Context, *Dependencies, string) error {
	return newStageError(CodeDockerWSLEndpoint, "e2e-preflight",
		errors.New("no docker-wsl setup state verifier is registered"))
}

type e2eOptions struct {
	Profile       string
	Label         string
	PodmanMode    string
	Binary        string
	Report        string
	StateFile     string
	Timeout       time.Duration
	FlakeAttempts int
}

// e2ePlan is the fully resolved, side-effect-free execution plan.
type e2ePlan struct {
	notes     []string
	preflight []Command
	suite     Command
	expect    reportExpectation
	report    string
	// stateFile is non-empty when the profile requires completed WSL setup.
	stateFile string
}

func newE2ECommand(deps *Dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "e2e", Short: "Run and verify E2E suites", Args: cobra.NoArgs}
	cmd.AddCommand(newE2ERunCommand(deps))
	return cmd
}

func newE2ERunCommand(deps *Dependencies) *cobra.Command {
	opts := e2eOptions{
		Profile:       profileLegacy,
		PodmanMode:    podmanModeNone,
		Timeout:       defaultE2ETimeout,
		FlakeAttempts: 1,
	}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a compiled E2E binary for one label and verify its Ginkgo JSON report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runE2E(cmd.Context(), deps, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Profile, "profile", opts.Profile,
		"runtime profile: legacy, linux-podman-rootless or docker-wsl-ssh")
	f.StringVar(&opts.Label, "label", "", "Ginkgo label to run, verbatim from the matrix")
	f.StringVar(&opts.PodmanMode, "podman-mode", opts.PodmanMode,
		"existing install-podman matrix value: none, true, rootful or rootless")
	f.StringVar(&opts.Binary, "binary", "", "E2E test binary (default ./e2e.test[.exe] per OS)")
	f.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "suite timeout passed as -test.timeout")
	f.IntVar(&opts.FlakeAttempts, "flake-attempts", opts.FlakeAttempts, "Ginkgo flake attempts")
	f.StringVar(&opts.Report, "report", "", "Ginkgo JSON report path to write and verify")
	f.StringVar(&opts.StateFile, "state-file", "",
		"docker-wsl setup state file (default $RUNNER_TEMP/devsy-ci/docker-wsl-state.json)")
	_ = cmd.MarkFlagRequired("label")
	_ = cmd.MarkFlagRequired("report")
	return cmd
}

func runE2E(ctx context.Context, deps *Dependencies, opts e2eOptions) error {
	plan, err := buildE2EPlan(deps, opts)
	if err != nil {
		return err
	}
	if plan.stateFile != "" {
		if err := dockerWSLPreflight(ctx, deps, plan.stateFile); err != nil {
			return err
		}
	}
	for _, note := range plan.notes {
		_, _ = fmt.Fprintln(deps.Out, note)
	}
	if err := runPreflight(ctx, deps, plan); err != nil {
		return err
	}
	// A stale report must never satisfy this run.
	if err := deps.Files.Remove(plan.report); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return newStageError(CodeE2EPreflightFailed, "e2e-preflight", err)
	}

	start := deps.Clock.Now()
	res, runErr := deps.Run.Run(ctx, plan.suite)
	elapsed := deps.Clock.Since(start)
	testErr := classifySuiteResult(res, runErr, elapsed, plan.report)
	reportErr := verifyReport(deps, plan, elapsed)
	return errors.Join(testErr, reportErr)
}

func runPreflight(ctx context.Context, deps *Dependencies, plan e2ePlan) error {
	for _, cmd := range plan.preflight {
		cmd.Stdout, cmd.Stderr = deps.Out, deps.Err
		start := deps.Clock.Now()
		res, err := deps.Run.Run(ctx, cmd)
		if err == nil && res.ExitCode == 0 && !res.TimedOut {
			continue
		}
		cause := err
		if cause == nil {
			cause = fmt.Errorf("%s failed", filepath.Base(cmd.Path))
		}
		se := newStageError(CodeE2EPreflightFailed, "e2e-preflight", cause)
		se.Elapsed, se.Detail = deps.Clock.Since(
			start,
		), tailBytes(
			redact(res.Stderr),
			maxDetailBytes,
		)
		if !res.TimedOut {
			se.ExitCode = &res.ExitCode
		}
		return se
	}
	return nil
}

func classifySuiteResult(
	res CommandResult,
	runErr error,
	elapsed time.Duration,
	report string,
) error {
	const stage = "e2e-test"
	switch {
	case runErr != nil:
		return newStageError(CodeE2ETestFailed, stage, runErr).WithElapsed(elapsed)
	case res.TimedOut:
		return newStageError(CodeE2ETestTimeout, stage,
			errors.New("suite did not exit before the hard deadline and was killed")).
			WithElapsed(elapsed).WithDetail(res.Stderr)
	case res.ExitCode == 0:
		return nil
	}
	detail := res.Stderr
	if strings.TrimSpace(detail) == "" {
		detail = res.Stdout
	}
	code, cause := CodeE2ETestFailed, errors.New("test binary exited nonzero")
	if strings.Contains(res.Stderr, "panic: test timed out") {
		code, cause = CodeE2ETestTimeout, errors.New("test binary hit its -test.timeout")
	}
	return newStageError(code, stage, cause).
		WithElapsed(elapsed).WithExitCode(res.ExitCode).WithArtifact(report).WithDetail(detail)
}

// verifyReport parses the report after the child exited, whatever its status,
// so a nonzero exit and an invalid report are both surfaced.
func verifyReport(deps *Dependencies, plan e2ePlan, elapsed time.Duration) error {
	const stage = "e2e-report"
	invalid := func(err error) error {
		return newStageError(CodeE2EReportInvalid, stage, err).
			WithElapsed(elapsed).WithArtifact(plan.report)
	}
	info, err := deps.Files.Stat(plan.report)
	if err != nil {
		return invalid(fmt.Errorf("report not produced: %w", err))
	}
	if info.Size() > maxReportBytes {
		return invalid(fmt.Errorf("report is %d bytes, over the %d byte limit",
			info.Size(), maxReportBytes))
	}
	data, err := deps.Files.ReadFile(plan.report)
	if err != nil {
		return invalid(err)
	}
	summary, verr := validateReport(data, plan.expect)
	if summary.Total > 0 {
		text := formatSummary(plan.report, summary)
		_, _ = fmt.Fprint(deps.Out, text)
		summaryPath := strings.TrimSuffix(plan.report, filepath.Ext(plan.report)) + ".summary.txt"
		if werr := deps.Files.WriteFile(summaryPath, []byte(text), 0o644); werr != nil {
			_, _ = fmt.Fprintf(deps.Err, "warning: write %s: %v\n", summaryPath, werr)
		}
	}
	if verr != nil {
		return invalid(verr)
	}
	return nil
}

// buildE2EPlan validates the options for the host and resolves argv and env.
func buildE2EPlan(deps *Dependencies, opts e2eOptions) (e2ePlan, error) {
	if err := validateCommon(opts); err != nil {
		return e2ePlan{}, err
	}
	plan, err := planForProfile(deps, opts)
	if err != nil {
		return e2ePlan{}, err
	}
	return finishPlan(deps, opts, plan)
}

func planForProfile(deps *Dependencies, opts e2eOptions) (e2ePlan, error) {
	mode := opts.PodmanMode
	if mode == "" {
		mode = podmanModeNone
	}
	env := deps.Environ()
	switch opts.Profile {
	case profileLegacy:
		return planLegacy(deps, env, mode)
	case profileLinuxPodmanRootless:
		return planRootless(deps, env, mode)
	case profileDockerWSLSSH:
		return planDockerWSL(deps, env, mode, opts)
	case profileStandard:
		return e2ePlan{}, unsupportedProfileError(
			"profile %q is not available; select %s, %s or %s",
			profileStandard, profileLegacy, profileLinuxPodmanRootless, profileDockerWSLSSH)
	default:
		return e2ePlan{}, configError("unknown profile %q", opts.Profile)
	}
}

func validateCommon(opts e2eOptions) error {
	switch {
	case !labelPattern.MatchString(opts.Label):
		return configError("invalid --label %q: want letters, digits, '.', '_' or '-'", opts.Label)
	case strings.TrimSpace(opts.Report) == "":
		return configError("--report is required")
	case opts.Timeout <= 0:
		return configError("--timeout must be positive, got %s", opts.Timeout)
	case opts.FlakeAttempts < 1:
		return configError("--flake-attempts must be at least 1, got %d", opts.FlakeAttempts)
	}
	switch opts.PodmanMode {
	case "", podmanModeNone, podmanModeTrue, podmanModeRootful, podmanModeRootless:
		return nil
	default:
		return configError("invalid --podman-mode %q: want none, true, rootful or rootless",
			opts.PodmanMode)
	}
}

// finishPlan resolves paths and appends the suite command shared by profiles.
// plan.suite carries Path/Args prefix/Env from the profile; its trailing test
// argv is added here so every profile runs the identical flag set.
func finishPlan(deps *Dependencies, opts e2eOptions, plan e2ePlan) (e2ePlan, error) {
	binary, err := resolveBinary(deps, opts.Binary)
	if err != nil {
		return e2ePlan{}, err
	}
	report, err := filepath.Abs(opts.Report)
	if err != nil {
		return e2ePlan{}, configError("resolve --report: %v", err)
	}
	plan.report = report
	if plan.suite.Path == "" {
		plan.suite.Path = binary
	} else {
		plan.suite.Args = append(plan.suite.Args, binary)
	}
	plan.suite.Args = append(plan.suite.Args, suiteArgs(opts, report)...)
	plan.suite.Timeout = opts.Timeout + suiteKillGrace
	plan.expect.Label = opts.Label
	return plan, nil
}

func suiteArgs(opts e2eOptions, report string) []string {
	return []string{
		"-test.v",
		"-ginkgo.v",
		"-test.timeout", opts.Timeout.String(),
		"-ginkgo.fail-on-empty",
		"-ginkgo.flake-attempts=" + strconv.Itoa(opts.FlakeAttempts),
		"--ginkgo.label-filter=" + opts.Label,
		"--ginkgo.json-report=" + report,
	}
}

func resolveBinary(deps *Dependencies, binary string) (string, error) {
	if binary == "" {
		binary = "./e2e.test"
		if deps.GOOS == goosWindows {
			binary = "./e2e.test.exe"
		}
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return "", configError("resolve --binary: %v", err)
	}
	info, err := deps.Files.Stat(abs)
	if err != nil {
		return "", configError("test binary %s: %v", abs, err)
	}
	if info.IsDir() {
		return "", configError("test binary %s is a directory", abs)
	}
	return abs, nil
}

func requireGOOS(deps *Dependencies, profile, want string) error {
	if deps.GOOS != want {
		return unsupportedProfileError(
			"profile %q requires %s, host is %s",
			profile,
			want,
			deps.GOOS,
		)
	}
	return nil
}

// planLegacy preserves the pre-existing per-platform behavior exactly.
func planLegacy(deps *Dependencies, env []string, mode string) (e2ePlan, error) {
	switch deps.GOOS {
	case goosLinux:
		if mode == podmanModeRootless {
			return rootlessPlan(deps, env), nil
		}
		return sudoPlan(env), nil
	case goosWindows:
		if mode == podmanModeRootless {
			return e2ePlan{}, configError("--podman-mode rootless is Linux only, host is windows")
		}
		overlay := append(forwardedEnv(env, deps.GOOS), "DOCKER_HOST="+legacyWindowsDockerHost)
		return e2ePlan{suite: Command{Env: overlay}}, nil
	default:
		return e2ePlan{}, unsupportedProfileError(
			"profile %q supports linux and windows, host is %s", profileLegacy, deps.GOOS)
	}
}

func planRootless(deps *Dependencies, env []string, mode string) (e2ePlan, error) {
	if err := requireGOOS(deps, profileLinuxPodmanRootless, goosLinux); err != nil {
		return e2ePlan{}, err
	}
	if mode != podmanModeNone && mode != podmanModeRootless {
		return e2ePlan{}, configError(
			"profile %q conflicts with --podman-mode %q", profileLinuxPodmanRootless, mode)
	}
	return rootlessPlan(deps, env), nil
}

func planDockerWSL(
	deps *Dependencies,
	env []string,
	mode string,
	opts e2eOptions,
) (e2ePlan, error) {
	if err := requireGOOS(deps, profileDockerWSLSSH, goosWindows); err != nil {
		return e2ePlan{}, err
	}
	if mode != podmanModeNone {
		return e2ePlan{}, configError(
			"profile %q conflicts with --podman-mode %q", profileDockerWSLSSH, mode)
	}
	if opts.FlakeAttempts != 1 {
		return e2ePlan{}, configError(
			"profile %q requires --flake-attempts 1 so configuration errors are never retried",
			profileDockerWSLSSH)
	}
	state := opts.StateFile
	if state == "" {
		state = defaultDockerWSLStateFile(env, deps.GOOS)
	}
	if state == "" {
		return e2ePlan{}, configError(
			"profile %q needs --state-file or RUNNER_TEMP", profileDockerWSLSSH)
	}
	return e2ePlan{
		suite: Command{Env: []string{"DOCKER_HOST=" + deadDockerHost, "DOCKER_CONTEXT="}},
		expect: reportExpectation{
			Required: append([]string(nil), dockerWSLRequiredSpecs...),
		},
		stateFile: state,
	}, nil
}

// defaultDockerWSLStateFile is the setup/cleanup state location under
// RUNNER_TEMP, or empty when RUNNER_TEMP is unset.
func defaultDockerWSLStateFile(env []string, goos string) string {
	temp, _ := lookupEnv(env, "RUNNER_TEMP", goos == goosWindows)
	if temp == "" {
		return ""
	}
	return filepath.Join(temp, "devsy-ci", "docker-wsl-state.json")
}

// rootlessPlan runs as the current user, no sudo, after `podman info`.
func rootlessPlan(deps *Dependencies, env []string) e2ePlan {
	home, _ := lookupEnv(env, "HOME", false)
	xdg, _ := lookupEnv(env, "XDG_RUNTIME_DIR", false)
	return e2ePlan{
		notes: []string{"Podman rootless test identity:", "HOME=" + home, "XDG_RUNTIME_DIR=" + xdg},
		preflight: []Command{
			{Path: "id", Timeout: preflightLimit},
			{Path: "podman", Args: []string{"info"}, Timeout: preflightLimit},
		},
		suite: Command{Env: forwardedEnv(env, deps.GOOS)},
	}
}

// sudoPlan reproduces the old whitelist exactly: sudo with explicit NAME=value
// arguments, never -E. DOCKER_HOST is forwarded only when non-empty.
func sudoPlan(env []string) e2ePlan {
	get := func(name string) string {
		v, _ := lookupEnv(env, name, false)
		return v
	}
	args := []string{
		"GH_USERNAME=" + get("GH_USERNAME"),
		"GH_ACCESS_TOKEN=" + get("GH_ACCESS_TOKEN"),
		"GH_CREDENTIAL_USERNAME=" + get("GH_CREDENTIAL_USERNAME"),
		"KUBECONFIG=" + kubeconfig(env, false),
	}
	if host := get("DOCKER_HOST"); host != "" {
		args = append(args, "DOCKER_HOST="+host)
	}
	args = append(args, "PATH="+get("PATH"))
	return e2ePlan{suite: Command{Path: "sudo", Args: args}}
}

// forwardedEnv is the overlay of the unprivileged legacy paths. It re-states
// the old explicit assignments (empty when unset) and the KUBECONFIG default.
func forwardedEnv(env []string, goos string) []string {
	fold := goos == goosWindows
	get := func(name string) string {
		v, _ := lookupEnv(env, name, fold)
		return v
	}
	return []string{
		"GH_USERNAME=" + get("GH_USERNAME"),
		"GH_ACCESS_TOKEN=" + get("GH_ACCESS_TOKEN"),
		"GH_CREDENTIAL_USERNAME=" + get("GH_CREDENTIAL_USERNAME"),
		"KUBECONFIG=" + kubeconfig(env, fold),
		"PATH=" + get("PATH"),
	}
}

// kubeconfig is ${KUBECONFIG:-$HOME/.kube/config}.
func kubeconfig(env []string, fold bool) string {
	if v, _ := lookupEnv(env, "KUBECONFIG", fold); v != "" {
		return v
	}
	home, _ := lookupEnv(env, "HOME", fold)
	return filepath.Join(home, ".kube", "config")
}

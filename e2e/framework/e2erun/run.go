package e2erun

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

	"github.com/devsy-org/devsy/e2e/framework/dockerwsl"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/specreport"
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

	// legacyWindowsDockerHost is the existing Podman machine pipe of Windows rows.
	legacyWindowsDockerHost = "npipe:////./pipe/podman-machine-default"

	defaultE2ETimeout = 1500 * time.Second
	// suiteKillGrace lets the test binary's own -test.timeout fire first.
	suiteKillGrace = 30 * time.Second
	preflightLimit = 2 * time.Minute
)

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SetupVerifier proves, from the state file at stateFile, that the runtime a
// profile needs was fully provisioned. It runs before the suite starts.
type SetupVerifier func(ctx context.Context, deps *runenv.Dependencies, stateFile string) error

// verifySetup runs verify and fails closed when no verifier was supplied.
func verifySetup(
	ctx context.Context,
	deps *runenv.Dependencies,
	verify SetupVerifier,
	stateFile string,
) error {
	if verify == nil {
		return runenv.NewStageError(runenv.CodeDockerWSLEndpoint, "e2e-preflight",
			errors.New("no docker-wsl setup state verifier was supplied"))
	}
	return verify(ctx, deps, stateFile)
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
	preflight []runenv.Command
	suite     runenv.Command
	expect    specreport.Expectation
	report    string
	// stateFile is non-empty when the profile requires completed WSL setup.
	stateFile string
}

func NewCommand(deps *runenv.Dependencies, verify SetupVerifier) *cobra.Command {
	cmd := &cobra.Command{Use: "e2e", Short: "Run and verify E2E suites", Args: cobra.NoArgs}
	cmd.AddCommand(newE2ERunCommand(deps, verify))
	return cmd
}

func newE2ERunCommand(deps *runenv.Dependencies, verify SetupVerifier) *cobra.Command {
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
			return runE2E(cmd.Context(), deps, verify, opts)
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

func runE2E(
	ctx context.Context,
	deps *runenv.Dependencies,
	verify SetupVerifier,
	opts e2eOptions,
) error {
	plan, err := buildE2EPlan(deps, opts)
	if err != nil {
		return err
	}
	if plan.stateFile != "" {
		if err := verifySetup(ctx, deps, verify, plan.stateFile); err != nil {
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
		return runenv.NewStageError(runenv.CodeE2EPreflightFailed, "e2e-preflight", err)
	}

	start := deps.Clock.Now()
	res, runErr := deps.Run.Run(ctx, plan.suite)
	elapsed := deps.Clock.Since(start)
	testErr := classifySuiteResult(res, runErr, elapsed, plan.report)
	reportErr := verifyReport(deps, plan, elapsed)
	return errors.Join(testErr, reportErr)
}

func runPreflight(ctx context.Context, deps *runenv.Dependencies, plan e2ePlan) error {
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
		se := runenv.NewStageError(runenv.CodeE2EPreflightFailed, "e2e-preflight", cause)
		se.Elapsed, se.Detail = deps.Clock.Since(
			start,
		), runenv.TailBytes(
			runenv.Redact(res.Stderr),
			runenv.MaxDetailBytes,
		)
		if !res.TimedOut {
			se.ExitCode = &res.ExitCode
		}
		return se
	}
	return nil
}

func classifySuiteResult(
	res runenv.CommandResult,
	runErr error,
	elapsed time.Duration,
	report string,
) error {
	const stage = "e2e-test"
	switch {
	case runErr != nil:
		return runenv.NewStageError(runenv.CodeE2ETestFailed, stage, runErr).WithElapsed(elapsed)
	case res.TimedOut:
		return runenv.NewStageError(runenv.CodeE2ETestTimeout, stage,
			errors.New("suite did not exit before the hard deadline and was killed")).
			WithElapsed(elapsed).WithDetail(res.Stderr)
	case res.ExitCode == 0:
		return nil
	}
	detail := res.Stderr
	if strings.TrimSpace(detail) == "" {
		detail = res.Stdout
	}
	code, cause := runenv.CodeE2ETestFailed, errors.New("test binary exited nonzero")
	if strings.Contains(res.Stderr, "panic: test timed out") {
		code, cause = runenv.CodeE2ETestTimeout, errors.New("test binary hit its -test.timeout")
	}
	return runenv.NewStageError(code, stage, cause).
		WithElapsed(elapsed).WithExitCode(res.ExitCode).WithArtifact(report).WithDetail(detail)
}

// verifyReport parses the report after the child exited, whatever its status,
// so a nonzero exit and an invalid report are both surfaced.
func verifyReport(deps *runenv.Dependencies, plan e2ePlan, elapsed time.Duration) error {
	const stage = "e2e-report"
	invalid := func(err error) error {
		return runenv.NewStageError(runenv.CodeE2EReportInvalid, stage, err).
			WithElapsed(elapsed).WithArtifact(plan.report)
	}
	info, err := deps.Files.Stat(plan.report)
	if err != nil {
		return invalid(fmt.Errorf("report not produced: %w", err))
	}
	if info.Size() > specreport.MaxReportBytes {
		return invalid(fmt.Errorf("report is %d bytes, over the %d byte limit",
			info.Size(), specreport.MaxReportBytes))
	}
	data, err := deps.Files.ReadFile(plan.report)
	if err != nil {
		return invalid(err)
	}
	summary, verr := specreport.Validate(data, plan.expect)
	if summary.Total > 0 {
		text := specreport.FormatSummary(plan.report, summary)
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
func buildE2EPlan(deps *runenv.Dependencies, opts e2eOptions) (e2ePlan, error) {
	if err := validateCommon(opts); err != nil {
		return e2ePlan{}, err
	}
	plan, err := planForProfile(deps, opts)
	if err != nil {
		return e2ePlan{}, err
	}
	return finishPlan(deps, opts, plan)
}

func planForProfile(deps *runenv.Dependencies, opts e2eOptions) (e2ePlan, error) {
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
		return e2ePlan{}, runenv.UnsupportedProfileError(
			"profile %q is not available; select %s, %s or %s",
			profileStandard, profileLegacy, profileLinuxPodmanRootless, profileDockerWSLSSH)
	default:
		return e2ePlan{}, runenv.ConfigError("unknown profile %q", opts.Profile)
	}
}

func validateCommon(opts e2eOptions) error {
	switch {
	case !labelPattern.MatchString(opts.Label):
		return runenv.ConfigError(
			"invalid --label %q: want letters, digits, '.', '_' or '-'",
			opts.Label,
		)
	case strings.TrimSpace(opts.Report) == "":
		return runenv.ConfigError("--report is required")
	case opts.Timeout <= 0:
		return runenv.ConfigError("--timeout must be positive, got %s", opts.Timeout)
	case opts.FlakeAttempts < 1:
		return runenv.ConfigError("--flake-attempts must be at least 1, got %d", opts.FlakeAttempts)
	}
	switch opts.PodmanMode {
	case "", podmanModeNone, podmanModeTrue, podmanModeRootful, podmanModeRootless:
		return nil
	default:
		return runenv.ConfigError("invalid --podman-mode %q: want none, true, rootful or rootless",
			opts.PodmanMode)
	}
}

// finishPlan resolves paths and appends the suite command shared by profiles.
// plan.suite carries Path/Args prefix/Env from the profile; its trailing test
// argv is added here so every profile runs the identical flag set.
func finishPlan(deps *runenv.Dependencies, opts e2eOptions, plan e2ePlan) (e2ePlan, error) {
	binary, err := resolveBinary(deps, opts.Binary)
	if err != nil {
		return e2ePlan{}, err
	}
	report, err := filepath.Abs(opts.Report)
	if err != nil {
		return e2ePlan{}, runenv.ConfigError("resolve --report: %v", err)
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

func resolveBinary(deps *runenv.Dependencies, binary string) (string, error) {
	if binary == "" {
		binary = "./e2e.test"
		if deps.GOOS == runenv.GoosWindows {
			binary = "./e2e.test.exe"
		}
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return "", runenv.ConfigError("resolve --binary: %v", err)
	}
	info, err := deps.Files.Stat(abs)
	if err != nil {
		return "", runenv.ConfigError("test binary %s: %v", abs, err)
	}
	if info.IsDir() {
		return "", runenv.ConfigError("test binary %s is a directory", abs)
	}
	return abs, nil
}

func requireGOOS(deps *runenv.Dependencies, profile, want string) error {
	if deps.GOOS != want {
		return runenv.UnsupportedProfileError(
			"profile %q requires %s, host is %s",
			profile,
			want,
			deps.GOOS,
		)
	}
	return nil
}

// planLegacy preserves the pre-existing per-platform behavior exactly.
func planLegacy(deps *runenv.Dependencies, env []string, mode string) (e2ePlan, error) {
	switch deps.GOOS {
	case runenv.GoosLinux:
		if mode == podmanModeRootless {
			return rootlessPlan(deps, env), nil
		}
		return sudoPlan(env), nil
	case runenv.GoosWindows:
		if mode == podmanModeRootless {
			return e2ePlan{}, runenv.ConfigError(
				"--podman-mode rootless is Linux only, host is windows",
			)
		}
		overlay := append(forwardedEnv(env, deps.GOOS), "DOCKER_HOST="+legacyWindowsDockerHost)
		return e2ePlan{suite: runenv.Command{Env: overlay}}, nil
	default:
		return e2ePlan{}, runenv.UnsupportedProfileError(
			"profile %q supports linux and windows, host is %s", profileLegacy, deps.GOOS)
	}
}

func planRootless(deps *runenv.Dependencies, env []string, mode string) (e2ePlan, error) {
	if err := requireGOOS(deps, profileLinuxPodmanRootless, runenv.GoosLinux); err != nil {
		return e2ePlan{}, err
	}
	if mode != podmanModeNone && mode != podmanModeRootless {
		return e2ePlan{}, runenv.ConfigError(
			"profile %q conflicts with --podman-mode %q", profileLinuxPodmanRootless, mode)
	}
	return rootlessPlan(deps, env), nil
}

func planDockerWSL(
	deps *runenv.Dependencies,
	env []string,
	mode string,
	opts e2eOptions,
) (e2ePlan, error) {
	if err := requireGOOS(deps, profileDockerWSLSSH, runenv.GoosWindows); err != nil {
		return e2ePlan{}, err
	}
	if mode != podmanModeNone {
		return e2ePlan{}, runenv.ConfigError(
			"profile %q conflicts with --podman-mode %q", profileDockerWSLSSH, mode)
	}
	if opts.FlakeAttempts != 1 {
		return e2ePlan{}, runenv.ConfigError(
			"profile %q requires --flake-attempts 1 so configuration errors are never retried",
			profileDockerWSLSSH)
	}
	state := opts.StateFile
	if state == "" {
		state = dockerwsl.DefaultStateFile(env, deps.GOOS)
	}
	if state == "" {
		return e2ePlan{}, runenv.ConfigError(
			"profile %q needs --state-file or RUNNER_TEMP", profileDockerWSLSSH)
	}
	return e2ePlan{
		suite: runenv.Command{
			Env: []string{"DOCKER_HOST=" + dockerwsl.DeadDockerHost, "DOCKER_CONTEXT="},
		},
		expect: specreport.Expectation{
			Required: append([]string(nil), dockerwsl.RequiredSpecs...),
		},
		stateFile: state,
	}, nil
}

// rootlessPlan runs as the current user, no sudo, after `podman info`.
func rootlessPlan(deps *runenv.Dependencies, env []string) e2ePlan {
	home, _ := runenv.LookupEnv(env, "HOME", false)
	xdg, _ := runenv.LookupEnv(env, "XDG_RUNTIME_DIR", false)
	return e2ePlan{
		notes: []string{"Podman rootless test identity:", "HOME=" + home, "XDG_RUNTIME_DIR=" + xdg},
		preflight: []runenv.Command{
			{Path: "id", Timeout: preflightLimit},
			{Path: "podman", Args: []string{"info"}, Timeout: preflightLimit},
		},
		suite: runenv.Command{Env: forwardedEnv(env, deps.GOOS)},
	}
}

// sudoPlan reproduces the old whitelist exactly: sudo with explicit NAME=value
// arguments, never -E. DOCKER_HOST is forwarded only when non-empty.
func sudoPlan(env []string) e2ePlan {
	get := func(name string) string {
		v, _ := runenv.LookupEnv(env, name, false)
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
	return e2ePlan{suite: runenv.Command{Path: "sudo", Args: args}}
}

// forwardedEnv is the overlay of the unprivileged legacy paths. It re-states
// the old explicit assignments (empty when unset) and the KUBECONFIG default.
func forwardedEnv(env []string, goos string) []string {
	fold := goos == runenv.GoosWindows
	get := func(name string) string {
		v, _ := runenv.LookupEnv(env, name, fold)
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
	if v, _ := runenv.LookupEnv(env, "KUBECONFIG", fold); v != "" {
		return v
	}
	home, _ := runenv.LookupEnv(env, "HOME", fold)
	return filepath.Join(home, ".kube", "config")
}

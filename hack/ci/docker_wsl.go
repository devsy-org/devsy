package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultDistribution   = "Ubuntu-24.04"
	defaultSetupTimeout   = 4 * time.Minute
	defaultCleanupTimeout = 2 * time.Minute
	// diagnosticsReserve is held back from the setup budget so a failure can
	// still be explained.
	diagnosticsReserve = 30 * time.Second

	envSSHDockerHost = "DEVSY_E2E_SSH_DOCKER_HOST"
	envWSLDaemonID   = "DEVSY_E2E_WSL_DAEMON_ID"
)

func init() {
	extraCommands = append(extraCommands, newDockerWSLCommand)
	dockerWSLPreflight = verifyDockerWSLSetup
}

// dockerWSLOptions are the resolved inputs shared by setup, cleanup and the
// e2e preflight.
type dockerWSLOptions struct {
	Distribution string
	StateFile    string
	RunID        string
	EndpointMode string
	Timeout      time.Duration
}

// dockerWSL provisions and tears down the WSL2 Docker + SSH fixture. All side
// effects go through deps and host.
type dockerWSL struct {
	deps  *Dependencies
	host  hostPlatform
	opts  dockerWSLOptions
	env   []string
	store stateStore
	gh    GitHubFiles
	guard stateGuard
	cli   dockerCLISource
	state *dockerWSLState
	tools map[string]string

	// In-memory results of earlier stages; each is re-derivable from the
	// distribution, so none needs to live in the state file.
	hostKey string
	vmIPs   []string
}

// newDockerWSL validates the options against the environment. It never touches
// the filesystem or runs a command.
func newDockerWSL(
	deps *Dependencies,
	host hostPlatform,
	opts dockerWSLOptions,
) (*dockerWSL, error) {
	if deps.GOOS != goosWindows {
		return nil, unsupportedProfileError(
			"docker-wsl requires native Windows, host is %s",
			deps.GOOS,
		)
	}
	env := deps.Environ()
	opts, err := resolveDockerWSLOptions(env, opts)
	if err != nil {
		return nil, err
	}
	profile, _ := lookupEnv(env, "USERPROFILE", true)
	userConfig := ""
	if profile != "" {
		userConfig = filepath.Join(profile, ".ssh", "config")
	}
	envPath, _ := lookupEnv(env, "GITHUB_ENV", true)
	pathPath, _ := lookupEnv(env, "GITHUB_PATH", true)
	return &dockerWSL{
		deps:  deps,
		host:  host,
		opts:  opts,
		env:   env,
		store: newStateStore(deps.Files, opts.StateFile),
		gh:    GitHubFiles{EnvPath: envPath, PathPath: pathPath, Files: deps.Files, Out: deps.Out},
		guard: stateGuard{
			runID:      opts.RunID,
			stateDir:   filepath.Dir(opts.StateFile),
			userConfig: userConfig,
		},
		cli:   pinnedDockerCLI,
		state: newDockerWSLState(opts.RunID, opts.Distribution),
		tools: map[string]string{},
	}, nil
}

func resolveDockerWSLOptions(env []string, opts dockerWSLOptions) (dockerWSLOptions, error) {
	if opts.StateFile == "" {
		opts.StateFile = defaultDockerWSLStateFile(env, goosWindows)
	}
	if opts.StateFile == "" {
		return opts, configError("docker-wsl needs --state-file or RUNNER_TEMP")
	}
	if opts.Distribution == "" {
		opts.Distribution = defaultDistribution
	}
	if !distroPattern.MatchString(opts.Distribution) {
		return opts, configError("invalid --distribution %q", opts.Distribution)
	}
	opts.RunID = sanitizeRunID(firstNonEmpty(opts.RunID, runIDFromEnv(env)))
	if !runIDPattern.MatchString(opts.RunID) {
		return opts, configError("invalid run id %q", opts.RunID)
	}
	if err := checkEndpointMode(&opts); err != nil {
		return opts, err
	}
	return opts, nil
}

func checkEndpointMode(opts *dockerWSLOptions) error {
	switch opts.EndpointMode {
	case "":
		opts.EndpointMode = sshModeAlias
	case sshModeAlias, sshModeDirect:
	default:
		return configError("invalid --endpoint-mode %q: want %s or %s",
			opts.EndpointMode, sshModeAlias, sshModeDirect)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// runIDFromEnv builds the per-job marker from the Actions run context.
func runIDFromEnv(env []string) string {
	var parts []string
	for _, name := range []string{"GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_JOB"} {
		if v, _ := lookupEnv(env, name, true); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return "local"
	}
	return strings.Join(parts, "-")
}

func newDockerWSLCommand(deps *Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docker-wsl",
		Short: "Provision and tear down the WSL2 Docker + SSH fixture",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newDockerWSLSetupCommand(deps), newDockerWSLCleanupCommand(deps))
	return cmd
}

func newDockerWSLSetupCommand(deps *Dependencies) *cobra.Command {
	opts := dockerWSLOptions{Timeout: defaultSetupTimeout, EndpointMode: sshModeAlias}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install Docker and sshd in WSL2, install the Windows CLI and prove the SSH route",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDockerWSL(cmd.Context(), deps, opts, (*dockerWSL).setup)
		},
	}
	f := cmd.Flags()
	f.StringVar(
		&opts.Distribution,
		"distribution",
		defaultDistribution,
		"WSL distribution to provision",
	)
	f.StringVar(
		&opts.StateFile,
		"state-file",
		"",
		"setup state file shared with cleanup (default $RUNNER_TEMP/devsy-ci/docker-wsl-state.json)",
	)
	f.StringVar(
		&opts.RunID,
		"run-id",
		"",
		"per-job marker (default from GITHUB_RUN_ID/ATTEMPT/JOB)",
	)
	f.StringVar(
		&opts.EndpointMode,
		"endpoint-mode",
		opts.EndpointMode,
		"ssh endpoint form: alias (ssh://devsyci@devsy-ci-<job>) or direct (ssh://devsyci@<ip>:2222)",
	)
	f.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "total setup budget")
	return cmd
}

func newDockerWSLCleanupCommand(deps *Dependencies) *cobra.Command {
	opts := dockerWSLOptions{Timeout: defaultCleanupTimeout}
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Tear down the fixture using the setup state file; fails if anything owned survives",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDockerWSL(cmd.Context(), deps, opts, (*dockerWSL).cleanup)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.StateFile, "state-file", "",
		"setup state file (default $RUNNER_TEMP/devsy-ci/docker-wsl-state.json)")
	f.StringVar(
		&opts.RunID,
		"run-id",
		"",
		"per-job marker (default from GITHUB_RUN_ID/ATTEMPT/JOB)",
	)
	f.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "total cleanup budget")
	return cmd
}

func runDockerWSL(
	ctx context.Context,
	deps *Dependencies,
	opts dockerWSLOptions,
	action func(*dockerWSL, context.Context) error,
) error {
	d, err := newDockerWSL(deps, newHostPlatform(deps), opts)
	if err != nil {
		return err
	}
	return action(d, ctx)
}

// verifyDockerWSLSetup is the e2e preflight: the state file must record a
// completed setup of this run and the exported endpoint must match it.
func verifyDockerWSLSetup(ctx context.Context, deps *Dependencies, stateFile string) error {
	d, err := newDockerWSL(deps, newHostPlatform(deps), dockerWSLOptions{StateFile: stateFile})
	if err != nil {
		return err
	}
	return d.preflight(ctx)
}

// preflight proves setup finished, the environment carries its exports and the
// WSL daemon still answers with the recorded ID.
func (d *dockerWSL) preflight(ctx context.Context) error {
	const stage = "e2e-preflight"
	bad := func(err error) error { return newStageError(CodeDockerWSLEndpoint, stage, err) }
	st, err := d.store.load()
	if err != nil {
		return bad(
			fmt.Errorf("no completed docker-wsl setup (run `docker-wsl setup` first): %w", err),
		)
	}
	if err := d.guard.validate(st); err != nil {
		return bad(err)
	}
	if st.Stage != stageVerified {
		return bad(fmt.Errorf("setup stopped at stage %q; it never completed", st.Stage))
	}
	if err := d.checkExports(st); err != nil {
		return bad(err)
	}
	d.state = st
	ctx, cancel := context.WithTimeout(ctx, wslQuickTimeout)
	defer cancel()
	id, err := d.wslDaemonID(ctx, stage)
	if err != nil {
		return err
	}
	if id != st.DaemonID {
		return bad(fmt.Errorf("WSL daemon ID %s differs from the recorded %s", id, st.DaemonID))
	}
	return nil
}

func (d *dockerWSL) checkExports(st *dockerWSLState) error {
	want := map[string]string{envSSHDockerHost: st.SSHEndpoint, envWSLDaemonID: st.DaemonID}
	for name, value := range want {
		got, _ := lookupEnv(d.env, name, true)
		if value == "" || got != value {
			return fmt.Errorf("%s is %q, setup recorded %q", name, got, value)
		}
	}
	return nil
}

// setup runs W1-W6 under one budget. Each stage is persisted as it completes;
// on failure bounded diagnostics are saved and the state is left for cleanup.
func (d *dockerWSL) setup(ctx context.Context) error {
	done, err := d.initState()
	if err != nil || done {
		return err
	}
	budget := max(d.opts.Timeout-diagnosticsReserve, time.Second)
	stageCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	for _, st := range d.setupStages() {
		if err := d.runStage(stageCtx, st); err != nil {
			return d.failSetup(ctx, stageCtx, st.name, err)
		}
	}
	return nil
}

type setupStage struct {
	name string
	run  func(context.Context) error
}

func (d *dockerWSL) setupStages() []setupStage {
	return []setupStage{
		{stageValidate, d.validateEnvironment},
		{stageWSLCheck, d.preflightWSL},
		{stageInstall, d.installPackages},
		{stageDaemon, d.startDockerd},
		{stageCLIName, d.installDockerCLI},
		{stageKey, d.createIdentity},
		{stageSSHConf, d.configureSSHServer},
		{stageSSHStart, d.startSSHD},
		{stageEndpoint, d.configureSSHClient},
		{stageOracles, d.verifyOracles},
	}
}

func (d *dockerWSL) runStage(ctx context.Context, st setupStage) error {
	start := d.deps.Clock.Now()
	err := st.run(ctx)
	result := "ok"
	if err != nil {
		result = "failed"
	}
	_, _ = fmt.Fprintf(d.deps.Out, "stage=%s duration=%s attempt=1 result=%s\n",
		st.name, d.deps.Clock.Since(start).Round(time.Millisecond), result)
	return err
}

// failSetup converts a stage failure into the final error: a spent budget
// becomes a timeout, and bounded diagnostics are attached.
func (d *dockerWSL) failSetup(parent, stageCtx context.Context, stage string, err error) error {
	if parent.Err() == nil && errors.Is(stageCtx.Err(), context.DeadlineExceeded) {
		err = newStageError(CodeDockerWSLSetupTimeout, stage,
			fmt.Errorf("setup budget of %s exhausted: %w", d.opts.Timeout, err))
	}
	se, ok := errors.AsType[*StageError](err)
	if !ok {
		se = newStageError(CodeDockerWSLInstall, stage, err)
	}
	diagCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), diagnosticsReserve)
	defer cancel()
	if path := d.collectDiagnostics(diagCtx, "setup-failure:"+stage); path != "" {
		_ = se.WithArtifact(path)
	}
	return se
}

// record applies a mutation to the in-memory state and persists it atomically
// before the caller proceeds to the next acquisition.
func (d *dockerWSL) record(mutate func(*dockerWSLState)) error {
	mutate(d.state)
	return d.store.save(d.state)
}

// initState loads or creates the state file. It reports done=true when this run
// already completed setup (a repeated invocation is a no-op).
func (d *dockerWSL) initState() (bool, error) {
	st, err := d.store.load()
	if errors.Is(err, errStateMissing) {
		return false, d.store.save(d.state)
	}
	if err != nil {
		return false, err
	}
	if err := d.guard.validate(st); err != nil {
		return false, err
	}
	if st.Stage == stageVerified && st.Distribution == d.opts.Distribution {
		_, _ = fmt.Fprintf(
			d.deps.Out,
			"docker-wsl already set up for run %s; nothing to do\n",
			st.RunID,
		)
		return true, nil
	}
	return false, configError(
		"state %s records an unfinished setup (stage %q) of this run; run docker-wsl cleanup first",
		d.opts.StateFile, st.Stage)
}

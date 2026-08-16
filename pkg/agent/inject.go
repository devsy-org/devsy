package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/devsy/pkg/version"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

var (
	ErrBinaryNotFound = errors.New("agent binary not found")
	ErrInjectTimeout  = errors.New("injection timeout")
	ErrArchMismatch   = errors.New("architecture mismatch")
)

const (
	osLinux    = "linux"
	statusDone = "done"
)

var (
	waitForInstanceConnectionTimeout = time.Minute * 5
	versionCheckTimeout              = time.Second * 30
)

// InjectOptions defines the parameters for injecting the Devsy agent into a remote environment.
type InjectOptions struct {
	// Exec is the function used to execute commands on the remote machine. Required.
	// nolint:staticcheck
	Exec inject.ExecFunc
	// IsLocal indicates if the injection target is the local machine.
	IsLocal bool
	// RemoteAgentPath is the path where the agent binary should be placed on the remote machine.
	RemoteAgentPath string
	// DownloadURL is the base URL to download the agent binary from.
	DownloadURL string
	// PreferDownloadFromRemoteUrl forces downloading the agent even if a local binary is available.
	// Defaults to true for release versions, false for dev versions.
	PreferDownloadFromRemoteUrl *bool
	// Timeout is the maximum duration to wait for the injection to complete. Defaults to 5 minutes.
	Timeout time.Duration
	// LocalVersion is the version of the local Devsy binary.
	// Defaults to version.GetVersion().
	LocalVersion string
	// RemoteVersion is the expected version of the remote agent.
	// Defaults to LocalVersion.
	RemoteVersion string
	// SkipVersionCheck disables the validation of the remote agent's version.
	// Defaults to false, unless DEVSY_AGENT_URL is set.
	SkipVersionCheck bool
	// Command is the command to execute upon successful injection.
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func (o *InjectOptions) ApplyDefaults() {
	o.applyPathDefaults()
	o.applyURLDefaults()
	o.applyPreferDownloadDefaults()
}

func (o *InjectOptions) Validate() error {
	if o.Exec == nil {
		return fmt.Errorf("exec function is required")
	}
	return nil
}

func (o *InjectOptions) applyPathDefaults() {
	if o.RemoteAgentPath == "" {
		o.RemoteAgentPath = config.RemoteDevsyHelperLocation
	}
	if o.Timeout == 0 {
		o.Timeout = waitForInstanceConnectionTimeout
	}
	if o.LocalVersion == "" {
		o.LocalVersion = version.GetVersion()
	}
	if o.RemoteVersion == "" {
		o.RemoteVersion = o.LocalVersion
	}
}

func (o *InjectOptions) applyURLDefaults() {
	if o.DownloadURL == "" {
		o.DownloadURL = config.DefaultAgentDownloadURL()
	}

	if strings.Contains(o.DownloadURL, "github.com") &&
		strings.Contains(o.DownloadURL, "/releases/tag/") {
		normalizedDownloadUrl := strings.Replace(
			o.DownloadURL,
			"/releases/tag/",
			"/releases/download/",
			1,
		)
		log.Warnf(
			"download URL %s is a tag URL, normalizing to download URL %s",
			o.DownloadURL,
			normalizedDownloadUrl,
		)
		o.DownloadURL = normalizedDownloadUrl
	}
}

func (o *InjectOptions) applyPreferDownloadDefaults() {
	if o.PreferDownloadFromRemoteUrl != nil {
		return
	}

	isDefaultURL := o.DownloadURL == config.DefaultAgentDownloadURL()
	hasCustomAgentURL := os.Getenv(config.EnvAgentURL) != "" || !isDefaultURL

	preferDownloadEnv := os.Getenv(config.EnvAgentPreferDownload)
	switch {
	case preferDownloadEnv != "":
		o.applyEnvPreference(preferDownloadEnv)
	case hasCustomAgentURL:
		o.PreferDownloadFromRemoteUrl = new(true)
		o.SkipVersionCheck = true
	case version.GetVersion() == version.DevVersion:
		o.PreferDownloadFromRemoteUrl = new(false)
		o.SkipVersionCheck = true
	default:
		o.PreferDownloadFromRemoteUrl = new(true)
	}
}

func (o *InjectOptions) applyEnvPreference(preferDownloadEnv string) {
	pref, err := strconv.ParseBool(preferDownloadEnv)
	if err != nil {
		log.Warnf("failed to parse %s, using default", config.EnvAgentPreferDownload)
		pref = true
	}
	o.PreferDownloadFromRemoteUrl = new(pref)
	o.SkipVersionCheck = true
}

func InjectAgent(ctx context.Context, opts *InjectOptions) error {
	opts.ApplyDefaults()
	if err := opts.Validate(); err != nil {
		return err
	}

	if opts.IsLocal {
		return injectLocally(ctx, opts)
	}

	vc := newVersionChecker(opts)
	bm, err := NewBinaryManager(opts.DownloadURL)
	if err != nil {
		return fmt.Errorf("create binary manager: %w", err)
	}

	backoff := wait.Backoff{
		Steps:    30,
		Duration: 10 * time.Second,
		Factor:   1.5,
		Jitter:   0.1,
		Cap:      60 * time.Second,
	}

	log.Debug("starting agent injection")
	return retry.OnError(backoff, func(err error) bool {
		if ctx.Err() != nil {
			return false
		}
		if errors.Is(err, docker.ErrContainerTerminal) {
			log.Errorf("container entered a terminal state, not retrying: %v", err)
			return false
		}
		log.Debugf("retrying injection: %v", err)
		return true
	}, func() error {
		return injectAgent(ctx, &injectConfig{
			opts: opts,
			bm:   bm,
			vc:   vc,
		})
	})
}

func injectLocally(ctx context.Context, opts *InjectOptions) error {
	if opts.Command == "" {
		return nil
	}
	log.Debug("execute command locally")
	return shell.RunEmulatedShell(ctx, &shell.CommandRunner{
		Command: opts.Command,
		Stdin:   opts.Stdin,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
		Environ: nil,
	})
}

type injectConfig struct {
	opts *InjectOptions
	bm   *BinaryManager
	vc   *versionChecker
}

func injectAgent(ctx context.Context, cfg *injectConfig) error {
	buf := &bytes.Buffer{}
	//nolint:staticcheck
	wasExecuted, err := inject.Inject(ctx, inject.InjectOptions{
		Exec:         cfg.opts.Exec,
		LocalFile:    createBinaryLoader(ctx, cfg),
		ScriptParams: buildScriptParams(cfg),
		Stdin:        cfg.opts.Stdin,
		Stdout:       cfg.opts.Stdout,
		Stderr:       setupStderr(cfg.opts, buf),
		Timeout:      cfg.opts.Timeout,
	})
	if err != nil {
		return handleInjectError(err, wasExecuted, buf)
	}

	return performVersionCheck(ctx, cfg)
}

func setupStderr(opts *InjectOptions, buf *bytes.Buffer) io.Writer {
	if opts.Stderr != nil {
		return io.MultiWriter(opts.Stderr, buf)
	}
	return buf
}

func createBinaryLoader(ctx context.Context, cfg *injectConfig) func(bool) (io.ReadCloser, error) {
	return func(arm bool) (io.ReadCloser, error) {
		arch := "amd64"
		if arm {
			arch = "arm64"
		}
		return cfg.bm.AcquireBinary(ctx, arch)
	}
}

func buildScriptParams(cfg *injectConfig) *inject.Params {
	opts := cfg.opts
	return &inject.Params{
		Command:             opts.Command,
		AgentRemotePath:     opts.RemoteAgentPath,
		DownloadURLs:        inject.NewDownloadURLs(opts.DownloadURL),
		ExistsCheck:         cfg.vc.buildExistsCheck(opts.RemoteAgentPath),
		PreferAgentDownload: *opts.PreferDownloadFromRemoteUrl,
		ShouldChmodPath:     true,
	}
}

func handleInjectError(err error, wasExecuted bool, buf *bytes.Buffer) error {
	if wasExecuted {
		return &InjectError{
			Stage: InjectStageCommandExecution,
			Cause: fmt.Errorf("%w: %s", err, buf.String()),
		}
	}
	return &InjectError{Stage: InjectStageInject, Cause: err}
}

func performVersionCheck(ctx context.Context, cfg *injectConfig) error {
	opts := cfg.opts

	detectedVersion, err := cfg.vc.detectRemoteAgentVersion(
		ctx,
		opts.Exec,
		opts.RemoteAgentPath,
	)

	if !opts.SkipVersionCheck {
		if err != nil {
			return &InjectError{Stage: InjectStageVersionCheck, Cause: err}
		}
	}

	if detectedVersion != "" && !opts.SkipVersionCheck {
		log.Debugf("detected remote agent version: %s", detectedVersion)
	}

	return nil
}

type InjectStage string

const (
	InjectStageInject           InjectStage = "inject"
	InjectStageCommandExecution InjectStage = "command execution"
	InjectStageVersionCheck     InjectStage = "version check"
)

type InjectError struct {
	Stage InjectStage
	Cause error
}

func (e *InjectError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %v", e.Stage, e.Cause)
	}
	return fmt.Sprintf("[%s] unknown error", e.Stage)
}

func (e *InjectError) Unwrap() error {
	return e.Cause
}

type versionChecker struct {
	localVersion  string
	remoteVersion string
	skipCheck     bool
}

func newVersionChecker(opts *InjectOptions) *versionChecker {
	return &versionChecker{
		localVersion:  opts.LocalVersion,
		remoteVersion: opts.RemoteVersion,
		skipCheck:     opts.SkipVersionCheck,
	}
}

func (vc *versionChecker) buildExistsCheck(agentPath string) string {
	if vc.skipCheck {
		return fmt.Sprintf(`! [ -x "%s" ]`, agentPath)
	}
	return fmt.Sprintf(`! { [ -x "%s" ] && [ "$("%s" --version 2>/dev/null)" = "%s" ]; }`,
		agentPath, agentPath, vc.remoteVersion)
}

// session encapsulates the state and resources for an injection session.
func (vc *versionChecker) detectRemoteAgentVersion(
	ctx context.Context,
	exec inject.ExecFunc,
	agentPath string,
) (string, error) {
	buf := &bytes.Buffer{}
	versionCmd := fmt.Sprintf("%s --version", agentPath)

	checkCtx, cancel := context.WithTimeout(ctx, versionCheckTimeout)
	defer cancel()
	err := exec(checkCtx, versionCmd, nil, buf, io.Discard)
	if err != nil {
		if errors.Is(checkCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return "", fmt.Errorf("get remote agent version timed out: %w", err)
		}
		return "", fmt.Errorf("failed to get remote agent version: %w", err)
	}

	actualVersion := strings.TrimSpace(buf.String())

	if vc.skipCheck {
		log.Debugf("skipping version validation, detected version: %s", actualVersion)
		return actualVersion, nil
	}

	if actualVersion != vc.remoteVersion {
		log.Warnf("the remote agent version does not match the expected version. "+
			"If your workspace fails to deploy, you may need to manually remove "+
			"the existing agent and redeploy: expectedVersion=%s, actualVersion=%s, agentPath=%s",
			vc.remoteVersion, actualVersion, agentPath)
	} else {
		log.Debug("remote agent version matches expected version")
	}

	return actualVersion, nil
}

package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"slices"
	"strings"
	"sync"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
)

// LifecyclePhase identifies a devcontainer lifecycle command.
type LifecyclePhase string

const (
	PhaseOnCreate      LifecyclePhase = "onCreateCommand"
	PhaseUpdateContent LifecyclePhase = "updateContentCommand"
	PhasePostCreate    LifecyclePhase = "postCreateCommand"
	PhasePostStart     LifecyclePhase = "postStartCommand"
	PhasePostAttach    LifecyclePhase = "postAttachCommand"
)

// DefaultWaitFor is the spec-defined default for the waitFor property.
const DefaultWaitFor = PhaseUpdateContent

// phaseOrder defines the canonical lifecycle ordering per the devcontainer spec.
var phaseOrder = []LifecyclePhase{
	PhaseOnCreate,
	PhaseUpdateContent,
	PhasePostCreate,
	PhasePostStart,
	PhasePostAttach,
}

// validWaitForPhase returns true when phase is an allowed waitFor value.
func validWaitForPhase(phase LifecyclePhase) bool {
	return slices.Contains(phaseOrder, phase)
}

// resolveWaitFor normalises the raw waitFor string from the config,
// falling back to the spec default for empty or invalid values.
func resolveWaitFor(raw string) LifecyclePhase {
	if raw == "" {
		return DefaultWaitFor
	}
	p := LifecyclePhase(raw)
	if !validWaitForPhase(p) {
		return DefaultWaitFor
	}
	return p
}

// hookRunParams groups the arguments for running a single lifecycle phase.
type hookRunParams struct {
	commands []types.LifecycleHook
	env      lifecycleEnv
	name     string
	content  string
}

// lifecycleEnv holds the resolved environment for running lifecycle hooks.
type lifecycleEnv struct {
	remoteUser      string
	workspaceFolder string
	remoteEnv       map[string]string
}

func resolveLifecycleEnv(
	ctx context.Context,
	setupInfo *config.Result,
) lifecycleEnv {
	mergedConfig := setupInfo.MergedConfig
	remoteUser := config.GetRemoteUser(setupInfo)
	probedEnv, err := config.ProbeUserEnv(ctx, mergedConfig.UserEnvProbe, remoteUser)
	if err != nil {
		log.Errorf(
			"failed to probe environment, this might lead to an incomplete setup of your workspace: error=%v",
			err,
		)
	}

	env := mergeRemoteEnv(mergedConfig.RemoteEnv, probedEnv, remoteUser)

	// Resolve declared secrets from the host environment.
	for name := range mergedConfig.Secrets {
		if val := os.Getenv(name); val != "" {
			env[name] = val
		}
	}

	return lifecycleEnv{
		remoteUser:      remoteUser,
		workspaceFolder: setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		remoteEnv:       env,
	}
}

// preAttachPhaseParams returns the hookRunParams for each pre-attach
// lifecycle phase in spec order.
func preAttachPhaseParams(
	setupInfo *config.Result,
	env lifecycleEnv,
	prebuild bool,
) []phaseHook {
	cd := setupInfo.ContainerDetails
	mc := setupInfo.MergedConfig

	updateContentMarker := cd.Created
	if prebuild {
		updateContentMarker = ""
	}

	return []phaseHook{
		{
			phase:  PhaseOnCreate,
			params: hookRunParams{mc.OnCreateCommands, env, "onCreateCommands", cd.Created},
		},
		{
			phase: PhaseUpdateContent,
			params: hookRunParams{
				mc.UpdateContentCommands, env, "updateContentCommands", updateContentMarker,
			},
		},
		{
			phase:  PhasePostCreate,
			params: hookRunParams{mc.PostCreateCommands, env, "postCreateCommands", cd.Created},
		},
		{
			phase: PhasePostStart,
			params: hookRunParams{
				mc.PostStartCommands, env, "postStartCommands", cd.State.StartedAt,
			},
		},
	}
}

// phaseHook pairs a lifecycle phase with the parameters needed to run it.
// When runFunc is set it is called instead of runHook (used for dotfiles).
type phaseHook struct {
	phase   LifecyclePhase
	params  hookRunParams
	runFunc func() error
}

// RunPreAttachHooks runs lifecycle hooks up to and including the waitFor phase
// synchronously and returns a slice of deferred phases that should run in the
// background. Dotfiles are installed between postCreateCommand and
// postStartCommand per the devcontainer spec.
//
// When prebuild is true, only onCreateCommand and updateContentCommand are
// executed and waitFor is ignored.
func RunPreAttachHooks(
	ctx context.Context,
	setupInfo *config.Result,
	prebuild bool,
	dotfiles DotfilesConfig,
) (DeferredHooks, error) {
	env := resolveLifecycleEnv(ctx, setupInfo)
	all := preAttachPhaseParams(setupInfo, env, prebuild)

	// Insert the dotfiles phase between postCreate and postStart.
	created := setupInfo.ContainerDetails.Created
	all = insertDotfilesPhase(ctx, all, dotfiles, created)

	if prebuild {
		return DeferredHooks{}, runPrebuildHooks(all)
	}

	waitFor := resolveWaitFor(setupInfo.MergedConfig.WaitFor)
	deferred, err := runWithWaitFor(all, waitFor)
	return DeferredHooks{hooks: deferred}, err
}

// insertDotfilesPhase splices a dotfiles phaseHook after postCreateCommand
// when a dotfiles repository is configured.
func insertDotfilesPhase(
	ctx context.Context,
	all []phaseHook,
	dotfiles DotfilesConfig,
	created string,
) []phaseHook {
	if dotfiles.Repository == "" {
		return all
	}

	idx := -1
	for i, ph := range all {
		if ph.phase == PhasePostCreate {
			idx = i + 1
			break
		}
	}
	if idx == -1 {
		idx = len(all)
	}

	cfg := dotfiles
	dotfilesHook := phaseHook{
		phase: PhaseDotfiles,
		params: hookRunParams{
			name:    "dotfilesInstall",
			content: created,
		},
		runFunc: func() error {
			skip, err := shouldSkipHook("dotfilesInstall", created)
			if err != nil || skip {
				return err
			}
			return RunDotfiles(ctx, cfg)
		},
	}

	return slices.Insert(all, idx, dotfilesHook)
}

// runPrebuildHooks runs only onCreateCommand and updateContentCommand.
func runPrebuildHooks(all []phaseHook) error {
	for _, ph := range all {
		if ph.phase != PhaseOnCreate && ph.phase != PhaseUpdateContent {
			continue
		}
		if err := runPhaseHook(ph); err != nil {
			return err
		}
	}
	return nil
}

// runWithWaitFor runs hooks up to and including waitFor synchronously
// and returns the remaining hooks as deferred.
func runWithWaitFor(
	all []phaseHook,
	waitFor LifecyclePhase,
) ([]phaseHook, error) {
	pastWaitFor := false
	var deferred []phaseHook

	for _, ph := range all {
		if pastWaitFor {
			deferred = append(deferred, ph)
			continue
		}
		if err := runPhaseHook(ph); err != nil {
			return nil, err
		}
		if ph.phase == waitFor {
			pastWaitFor = true
		}
	}

	return deferred, nil
}

// runPhaseHook dispatches to either the custom runFunc or the standard
// runHook depending on the phaseHook configuration.
func runPhaseHook(ph phaseHook) error {
	if ph.runFunc != nil {
		return ph.runFunc()
	}
	return runHook(ph.params)
}

// DeferredHooks holds lifecycle hooks that should run in the background
// after the foreground (waitFor) hooks have completed.
type DeferredHooks struct {
	hooks []phaseHook
}

// Empty returns true when there are no deferred hooks with work to run.
func (d DeferredHooks) Empty() bool {
	for _, ph := range d.hooks {
		if ph.runFunc != nil || len(ph.params.commands) > 0 {
			return false
		}
	}
	return true
}

// Run executes all deferred hooks sequentially.
func (d DeferredHooks) Run() error {
	for _, ph := range d.hooks {
		if err := runPhaseHook(ph); err != nil {
			return err
		}
	}
	return nil
}

// RunPostAttachHooks runs postAttachCommand only.
// These run after the IDE has been opened and can be long-running.
func RunPostAttachHooks(ctx context.Context, setupInfo *config.Result) error {
	env := resolveLifecycleEnv(ctx, setupInfo)

	return runHook(hookRunParams{
		commands: setupInfo.MergedConfig.PostAttachCommands,
		env:      env,
		name:     "postAttachCommands",
		content:  "",
	})
}

func runHook(p hookRunParams) error {
	if len(p.commands) == 0 {
		return nil
	}

	if skip, err := shouldSkipHook(p.name, p.content); err != nil || skip {
		return err
	}

	envArr := buildEnvArr(p.env.remoteEnv)
	return executeHookCommands(p, envArr)
}

func shouldSkipHook(name, content string) (bool, error) {
	if content == "" {
		return false, nil
	}
	return markerFileExists(name, content)
}

func buildEnvArr(remoteEnv map[string]string) []string {
	arr := make([]string, 0, len(remoteEnv))
	for k, v := range remoteEnv {
		arr = append(arr, k+"="+v)
	}
	return arr
}

func executeHookCommands(p hookRunParams, envArr []string) error {
	for _, cmd := range p.commands {
		if len(cmd) == 0 {
			continue
		}
		if err := executeLifecycleHook(p, envArr, cmd); err != nil {
			return err
		}
	}
	return nil
}

// executeLifecycleHook runs the sub-commands within a single LifecycleHook.
// When the hook has multiple named keys (object syntax), the sub-commands run
// concurrently per the devcontainer spec. Single-key hooks run directly.
func executeLifecycleHook(
	p hookRunParams,
	envArr []string,
	hook types.LifecycleHook,
) error {
	if len(hook) <= 1 {
		for k, c := range hook {
			return runSingleHookCommand(p, envArr, k, c)
		}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	wg.Add(len(hook))
	for k, c := range hook {
		go func() {
			defer wg.Done()
			if err := runSingleHookCommand(p, envArr, k, c); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("named command %q failed: %w", k, err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}

func runSingleHookCommand(
	p hookRunParams,
	remoteEnvArr []string,
	key string, c []string,
) error {
	log.Infof("running %s lifecycle hook: %s %s", p.name, key, strings.Join(c, " "))
	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	if len(c) == 0 {
		log.Debugf("skipping empty command for lifecycle hook %s", p.name)
		return nil
	}
	args := buildCommandArgs(c, p.env.remoteUser, currentUser.Username)

	resolvedPath, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("command not found: %s: %w", args[0], err)
	}

	cmd := &exec.Cmd{
		Path: resolvedPath,
		Args: args,
		Dir:  p.env.workspaceFolder,
		Env:  append(os.Environ(), remoteEnvArr...),
	}

	return executeAndLog(cmd, key, c)
}

func executeAndLog(cmd *exec.Cmd, key string, c []string) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		logPipeOutput(stdoutPipe, true)
	}()

	go func() {
		defer wg.Done()
		logPipeOutput(stderrPipe, false)
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		log.Debugf(
			"failed running %s lifecycle script: command=%v, error=%v",
			key,
			cmd.Args,
			err,
		)
		return fmt.Errorf("failed to run: %s, error: %w", strings.Join(c, " "), err)
	}

	log.Infof("ran command: command=%s, args=%s", key, strings.Join(c, " "))
	return nil
}

func logPipeOutput(pipe io.ReadCloser, isStdout bool) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		if isStdout {
			log.Info(line)
		} else {
			if containsError(line) {
				log.Error(line)
			} else {
				log.Warn(line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Errorf("error reading pipe: error=%v", err)
	}
}

// containsError defines what log line treated as error log should contain.
func containsError(line string) bool {
	return strings.Contains(strings.ToLower(line), "error")
}

func mergeRemoteEnv(
	remoteEnv map[string]*string,
	probedEnv map[string]string,
	remoteUser string,
) map[string]string {
	retEnv := map[string]string{}
	maps.Copy(retEnv, probedEnv)

	// Apply remoteEnv: nil means unset, non-nil means override.
	for k, v := range remoteEnv {
		if v == nil {
			delete(retEnv, k)
		} else {
			retEnv[k] = *v
		}
	}

	mergePATH(retEnv, remoteEnv, probedEnv, remoteUser)

	return retEnv
}

func mergePATH(
	retEnv map[string]string,
	remoteEnv map[string]*string,
	probedEnv map[string]string,
	remoteUser string,
) {
	remotePath, remoteOk := remoteEnv["PATH"]
	if !remoteOk {
		return
	}
	// nil PATH means unset — already handled by the delete above.
	if remotePath == nil {
		return
	}
	probedPath, probeOk := probedEnv["PATH"]
	if !probeOk {
		return
	}
	sbinRegex := regexp.MustCompile(`/sbin(/|$)`)
	probedTokens := strings.Split(probedPath, ":")
	insertAt := 0
	for e := range strings.SplitSeq(*remotePath, ":") {
		i := slices.Index(probedTokens, e)
		if i == -1 {
			if remoteUser == "root" || !sbinRegex.MatchString(e) {
				probedTokens = slices.Insert(probedTokens, insertAt, e)
			}
		} else {
			insertAt = i + 1
		}
	}
	retEnv["PATH"] = strings.Join(probedTokens, ":")
}

func buildCommandArgs(c []string, remoteUser, currentUsername string) []string {
	if len(c) == 1 {
		if remoteUser != currentUsername {
			return []string{"su", remoteUser, "-c", c[0]}
		}
		return []string{"sh", "-c", c[0]}
	}
	if remoteUser != currentUsername {
		return []string{"su", remoteUser, "-c", shellescape.QuoteCommand(c)}
	}
	return c
}

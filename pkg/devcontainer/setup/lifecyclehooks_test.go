package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

type LifecycleHookTestSuite struct {
	suite.Suite
}

func (s *LifecycleHookTestSuite) TestStringCommandWithQuotes() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{`echo "hello world"`}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"sh", "-c", `echo "hello world"`}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommand() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"echo", "hello", "world"}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"echo", "hello", "world"}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommandWithShellWrapper() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"sh", "-c", `echo "test"`}
	args := buildCommandArgs(c, currentUser.Username, currentUser.Username)
	assert.Equal(s.T(), []string{"sh", "-c", `echo "test"`}, args)
}

func (s *LifecycleHookTestSuite) TestStringCommandWithUserSwitch() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{`echo "hello"`}
	args := buildCommandArgs(c, "otheruser", currentUser.Username)
	assert.Equal(s.T(), []string{"su", "otheruser", "-c", `echo "hello"`}, args)
}

func (s *LifecycleHookTestSuite) TestArrayCommandWithUserSwitch() {
	currentUser, err := user.Current()
	s.Require().NoError(err)

	c := []string{"echo", "hello"}
	args := buildCommandArgs(c, "otheruser", currentUser.Username)
	assert.Equal(s.T(), []string{"su", "otheruser", "-c", "echo hello"}, args)
}

func (s *LifecycleHookTestSuite) TestSymlinkWithQuotes() {
	if os.Getuid() != 0 {
		s.T().Skip("Requires root")
	}

	testLink := "/tmp/devsy_test_link"
	_ = os.Remove(testLink)
	defer func() { _ = os.Remove(testLink) }()

	cmd := exec.Command("sh", "-c", `ln -sf "$(command -v ls)" `+testLink)
	output, err := cmd.CombinedOutput()
	s.Require().NoError(err, "Output: %s", output)

	target, err := os.Readlink(testLink)
	s.Require().NoError(err)
	s.Require().NotEmpty(target, "symlink target should not be empty")
	assert.NotEqual(s.T(), byte('"'), target[0])
	assert.NotEqual(s.T(), byte('"'), target[len(target)-1])
}

func (s *LifecycleHookTestSuite) TestLifecycleHooksNoOpWithEmptyConfig() {
	ctx := context.Background()
	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	// Both functions should return nil with empty config (no commands to run)
	deferred, err := RunPreAttachHooks(ctx, result, false, DotfilesConfig{}, nil, SkipPhases{})
	assert.NoError(s.T(), err)
	assert.True(s.T(), deferred.Empty())

	err = RunPostAttachHooks(ctx, result, nil)
	assert.NoError(s.T(), err)
}

func (s *LifecycleHookTestSuite) TestResolveLifecycleEnvIncludesSecrets() {
	t := s.T()

	// Set one secret in the environment, leave another absent.
	t.Setenv("SECRET_PRESENT", "s3cret")

	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				Secrets: map[string]config.SecretConfig{
					"SECRET_PRESENT": {Description: "a present secret"},
					"SECRET_ABSENT":  {Description: "a missing secret"},
				},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	env := resolveLifecycleEnv(context.Background(), result)

	assert.Equal(t, "s3cret", env.remoteEnv["SECRET_PRESENT"])
	_, found := env.remoteEnv["SECRET_ABSENT"]
	assert.False(t, found, "SECRET_ABSENT should not be in remoteEnv when not set in environment")
}

func (s *LifecycleHookTestSuite) TestResolveWaitForDefault() {
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor(""))
}

func (s *LifecycleHookTestSuite) TestResolveWaitForValid() {
	assert.Equal(s.T(), PhasePostCreate, resolveWaitFor("postCreateCommand"))
	assert.Equal(s.T(), PhasePostStart, resolveWaitFor("postStartCommand"))
	assert.Equal(s.T(), PhaseOnCreate, resolveWaitFor("onCreateCommand"))
	assert.Equal(s.T(), PhasePostAttach, resolveWaitFor("postAttachCommand"))
	assert.Equal(s.T(), PhaseUpdateContent, resolveWaitFor("updateContentCommand"))
	assert.Equal(s.T(), PhaseInitializeCommand, resolveWaitFor("initializeCommand"))
}

func (s *LifecycleHookTestSuite) TestResolveWaitForInvalid() {
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor("bogus"))
	assert.Equal(s.T(), DefaultWaitFor, resolveWaitFor("POSTCREATECOMMAND"))
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForDefaultSplit() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, DefaultWaitFor)
	assert.NoError(t, err)

	// Default waitFor is updateContentCommand.
	// Deferred should be postCreateCommand and postStartCommand.
	assert.Len(t, deferred, 2)
	assert.Equal(t, PhasePostCreate, deferred[0].phase)
	assert.Equal(t, PhasePostStart, deferred[1].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForPostCreate() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhasePostCreate)
	assert.NoError(t, err)

	// Only postStartCommand should be deferred.
	assert.Len(t, deferred, 1)
	assert.Equal(t, PhasePostStart, deferred[0].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForPostStart() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhasePostStart)
	assert.NoError(t, err)

	// All pre-attach hooks run in foreground, nothing deferred.
	assert.Empty(t, deferred)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForOnCreate() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhaseOnCreate)
	assert.NoError(t, err)

	// updateContentCommand, postCreateCommand, postStartCommand deferred.
	assert.Len(t, deferred, 3)
	assert.Equal(t, PhaseUpdateContent, deferred[0].phase)
	assert.Equal(t, PhasePostCreate, deferred[1].phase)
	assert.Equal(t, PhasePostStart, deferred[2].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForInitializeCommand() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhaseInitializeCommand)
	assert.NoError(t, err)

	// initializeCommand is a host-side phase; all container phases are deferred.
	assert.Len(t, deferred, len(all))
	assert.Equal(t, PhaseOnCreate, deferred[0].phase)
	assert.Equal(t, PhaseUpdateContent, deferred[1].phase)
	assert.Equal(t, PhasePostCreate, deferred[2].phase)
	assert.Equal(t, PhasePostStart, deferred[3].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForInitializeCommandSliceCopy() {
	t := s.T()
	all := makeTestPhaseHooks()

	deferred, err := runWithWaitFor(all, PhaseInitializeCommand)
	assert.NoError(t, err)

	// Mutating the deferred slice must not affect the original.
	deferred[0].phase = "mutated"
	assert.Equal(t, PhaseOnCreate, all[0].phase, "original slice must not be affected")
}

func (s *LifecycleHookTestSuite) TestPrebuildIgnoresWaitFor() {
	ctx := context.Background()
	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				// Set waitFor to onCreateCommand — prebuild should ignore this.
				WaitFor: "onCreateCommand",
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/test",
		},
	}

	// In prebuild mode, no deferred hooks are returned regardless of waitFor.
	deferred, err := RunPreAttachHooks(ctx, result, true, DotfilesConfig{}, nil, SkipPhases{})
	assert.NoError(s.T(), err)
	assert.True(s.T(), deferred.Empty())
}

func (s *LifecycleHookTestSuite) TestDeferredHooksEmpty() {
	d := DeferredHooks{}
	assert.True(s.T(), d.Empty())
	assert.NoError(s.T(), d.Run())
}

// makeTestPhaseHooks creates a phaseHook slice with no commands so that
// runHook is a no-op. This lets us test the split logic without executing
// real processes.
func makeTestPhaseHooks() []phaseHook {
	return []phaseHook{
		{phase: PhaseOnCreate, params: hookRunParams{name: "onCreateCommands"}},
		{phase: PhaseUpdateContent, params: hookRunParams{name: "updateContentCommands"}},
		{phase: PhasePostCreate, params: hookRunParams{name: "postCreateCommands"}},
		{phase: PhasePostStart, params: hookRunParams{name: "postStartCommands"}},
	}
}

func ptr(s string) *string { return &s }

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilUnsetsKey() {
	probedEnv := map[string]string{"KEEP": "yes", "DROP": "bye"}
	remoteEnv := map[string]*string{"DROP": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "yes", result["KEEP"])
	_, found := result["DROP"]
	assert.False(s.T(), found, "nil value should unset key")
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNonNilOverrides() {
	probedEnv := map[string]string{"VAR": "old"}
	remoteEnv := map[string]*string{"VAR": ptr("new")}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "new", result["VAR"])
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilMissingKeyNoop() {
	probedEnv := map[string]string{"OTHER": "val"}
	remoteEnv := map[string]*string{"ABSENT": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	assert.Equal(s.T(), "val", result["OTHER"])
	_, found := result["ABSENT"]
	assert.False(s.T(), found)
}

func (s *LifecycleHookTestSuite) TestMergeRemoteEnvNilPATHRemoves() {
	probedEnv := map[string]string{
		"PATH": "/usr/bin:/bin",
		"HOME": "/home/dev",
	}
	remoteEnv := map[string]*string{"PATH": nil}

	result := mergeRemoteEnv(remoteEnv, probedEnv, "vscode")

	_, found := result["PATH"]
	assert.False(s.T(), found, "nil PATH should remove PATH")
	assert.Equal(s.T(), "/home/dev", result["HOME"])
}

func (s *LifecycleHookTestSuite) TestParallelNamedCommandsTiming() {
	t := s.T()
	currentUser, err := user.Current()
	assert.NoError(t, err)

	// Two "sleep 0.5" commands that, if run in parallel, complete in ~0.5s.
	hook := types.LifecycleHook{
		"sleep-a": {"sleep", "0.5"},
		"sleep-b": {"sleep", "0.5"},
	}

	p := hookRunParams{
		commands: []types.LifecycleHook{hook},
		env: lifecycleEnv{
			remoteUser:      currentUser.Username,
			workspaceFolder: t.TempDir(),
		},
		name: "testParallel",
	}

	envArr := buildEnvArr(p.env.remoteEnv)

	start := time.Now()
	err = executeHookCommands(p, envArr)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, elapsed, 900*time.Millisecond,
		"two 0.5s commands should complete in ~0.5s when parallel, not ~1s")
}

func (s *LifecycleHookTestSuite) TestParallelNamedCommandsErrorIsolation() {
	t := s.T()
	currentUser, err := user.Current()
	assert.NoError(t, err)

	dir := t.TempDir()
	markerFile := filepath.Join(dir, "ran.txt")

	// "fail" exits immediately; "succeed" sleeps briefly then writes a marker.
	hook := types.LifecycleHook{
		"fail":    {"sh", "-c", "exit 1"},
		"succeed": {"sh", "-c", fmt.Sprintf("sleep 0.1 && echo done > %s", markerFile)},
	}

	p := hookRunParams{
		commands: []types.LifecycleHook{hook},
		env: lifecycleEnv{
			remoteUser:      currentUser.Username,
			workspaceFolder: dir,
		},
		name: "testErrorIsolation",
	}

	envArr := buildEnvArr(p.env.remoteEnv)
	err = executeHookCommands(p, envArr)

	// The combined error should mention which named command failed.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `named command "fail" failed`)

	// The succeed command should have run to completion despite the failure.
	_, statErr := os.Stat(markerFile)
	assert.NoError(t, statErr, "succeed command should run even when fail command errors")
}

func (s *LifecycleHookTestSuite) TestSingleStringCommandBackwardCompat() {
	t := s.T()
	currentUser, err := user.Current()
	assert.NoError(t, err)

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")

	// Anonymous string command uses key "".
	hook := types.LifecycleHook{
		"": {"sh", "-c", fmt.Sprintf("echo hello > %s", outFile)},
	}

	p := hookRunParams{
		commands: []types.LifecycleHook{hook},
		env: lifecycleEnv{
			remoteUser:      currentUser.Username,
			workspaceFolder: dir,
		},
		name: "testBackwardCompat",
	}

	envArr := buildEnvArr(p.env.remoteEnv)
	err = executeHookCommands(p, envArr)

	assert.NoError(t, err)
	assertFileContains(t, dir, "out.txt", "hello")
}

func (s *LifecycleHookTestSuite) TestSingleNamedCommandNoGoroutine() {
	t := s.T()
	currentUser, err := user.Current()
	assert.NoError(t, err)

	dir := t.TempDir()
	outFile := filepath.Join(dir, "named.txt")

	hook := types.LifecycleHook{
		"setup": {
			"sh", "-c",
			fmt.Sprintf("echo setup > %s", outFile),
		},
	}

	p := hookRunParams{
		commands: []types.LifecycleHook{hook},
		env: lifecycleEnv{
			remoteUser:      currentUser.Username,
			workspaceFolder: dir,
		},
		name: "testSingleNamed",
	}

	envArr := buildEnvArr(p.env.remoteEnv)
	err = executeHookCommands(p, envArr)

	assert.NoError(t, err)
	assertFileContains(t, dir, "named.txt", "setup")
}

// assertFileContains reads a file under dir by name and checks it
// contains the expected substring. Using filepath.Join with a
// known-safe base directory satisfies gosec G304.
func assertFileContains(
	t *testing.T,
	dir, name, expected string,
) {
	t.Helper()
	content, err := os.ReadFile(
		filepath.Clean(filepath.Join(dir, name)),
	)
	assert.NoError(t, err)
	assert.Contains(t, string(content), expected)
}

func (s *LifecycleHookTestSuite) TestInsertDotfilesPhaseOrdering() {
	t := s.T()
	all := makeTestPhaseHooks()
	ctx := context.Background()

	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}
	result := insertDotfilesPhase(ctx, all, cfg, "test-created")

	// Should have 5 phases: onCreate, updateContent, postCreate, dotfiles, postStart
	assert.Len(t, result, 5)
	assert.Equal(t, PhaseOnCreate, result[0].phase)
	assert.Equal(t, PhaseUpdateContent, result[1].phase)
	assert.Equal(t, PhasePostCreate, result[2].phase)
	assert.Equal(t, PhaseDotfiles, result[3].phase)
	assert.Equal(t, PhasePostStart, result[4].phase)
}

func (s *LifecycleHookTestSuite) TestInsertDotfilesPhaseSkippedWhenEmpty() {
	t := s.T()
	all := makeTestPhaseHooks()
	ctx := context.Background()

	result := insertDotfilesPhase(ctx, all, DotfilesConfig{}, "test-created")

	// No dotfiles repo — phase list unchanged.
	assert.Len(t, result, 4)
	for i, ph := range result {
		assert.Equal(t, all[i].phase, ph.phase)
	}
}

func (s *LifecycleHookTestSuite) TestDotfilesPhaseHasRunFunc() {
	t := s.T()
	all := makeTestPhaseHooks()
	ctx := context.Background()

	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}
	result := insertDotfilesPhase(ctx, all, cfg, "")

	dotfilesHook := result[3]
	assert.Equal(t, PhaseDotfiles, dotfilesHook.phase)
	assert.NotNil(t, dotfilesHook.runFunc, "dotfiles phase should have a runFunc")
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForDefaultSplitWithDotfiles() {
	t := s.T()
	all := makeTestPhaseHooks()
	ctx := context.Background()

	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}
	all = insertDotfilesPhase(ctx, all, cfg, "")

	deferred, err := runWithWaitFor(all, DefaultWaitFor)
	assert.NoError(t, err)

	// Default waitFor is updateContentCommand.
	// Deferred: postCreate, dotfiles, postStart.
	assert.Len(t, deferred, 3)
	assert.Equal(t, PhasePostCreate, deferred[0].phase)
	assert.Equal(t, PhaseDotfiles, deferred[1].phase)
	assert.Equal(t, PhasePostStart, deferred[2].phase)
}

func (s *LifecycleHookTestSuite) TestRunWithWaitForPostCreateWithDotfiles() {
	t := s.T()
	all := makeTestPhaseHooks()
	ctx := context.Background()

	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}
	all = insertDotfilesPhase(ctx, all, cfg, "")

	deferred, err := runWithWaitFor(all, PhasePostCreate)
	assert.NoError(t, err)

	// Deferred: dotfiles, postStart.
	assert.Len(t, deferred, 2)
	assert.Equal(t, PhaseDotfiles, deferred[0].phase)
	assert.Equal(t, PhasePostStart, deferred[1].phase)
}

func (s *LifecycleHookTestSuite) TestDeferredHooksNotEmptyWithDotfiles() {
	t := s.T()
	ctx := context.Background()

	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}
	all := insertDotfilesPhase(ctx, nil, cfg, "")

	d := DeferredHooks{hooks: all}
	assert.False(t, d.Empty(), "should not be empty when dotfiles runFunc is set")
}

func (s *LifecycleHookTestSuite) TestPromoteDotfilesWaitForDefault() {
	t := s.T()
	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}

	// Default waitFor (updateContentCommand) should be promoted to PhaseDotfiles.
	result := promoteDotfilesWaitFor(DefaultWaitFor, cfg)
	assert.Equal(t, PhaseDotfiles, result)
}

func (s *LifecycleHookTestSuite) TestPromoteDotfilesWaitForPostCreate() {
	t := s.T()
	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}

	// postCreateCommand is before dotfiles, so it should be promoted.
	result := promoteDotfilesWaitFor(PhasePostCreate, cfg)
	assert.Equal(t, PhaseDotfiles, result)
}

func (s *LifecycleHookTestSuite) TestPromoteDotfilesWaitForPostStartNotPromoted() {
	t := s.T()
	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}

	// postStartCommand is after dotfiles, no promotion needed.
	result := promoteDotfilesWaitFor(PhasePostStart, cfg)
	assert.Equal(t, PhasePostStart, result)
}

func (s *LifecycleHookTestSuite) TestPromoteDotfilesWaitForInitializeCommandNotPromoted() {
	t := s.T()
	cfg := DotfilesConfig{Repository: "https://github.com/user/dotfiles"}

	// initializeCommand defers everything; dotfiles promotion must not override it.
	result := promoteDotfilesWaitFor(PhaseInitializeCommand, cfg)
	assert.Equal(t, PhaseInitializeCommand, result)
}

func (s *LifecycleHookTestSuite) TestPromoteDotfilesWaitForNoDotfiles() {
	t := s.T()

	// No dotfiles configured — no promotion regardless of waitFor.
	result := promoteDotfilesWaitFor(DefaultWaitFor, DotfilesConfig{})
	assert.Equal(t, DefaultWaitFor, result)
}

func (s *LifecycleHookTestSuite) TestPhaseHasCommandsTrue() {
	all := []phaseHook{
		{
			phase:  PhaseOnCreate,
			params: hookRunParams{commands: []types.LifecycleHook{{"": {"echo", "hi"}}}},
		},
	}
	assert.True(s.T(), phaseHasCommands(all, PhaseOnCreate))
}

func (s *LifecycleHookTestSuite) TestPhaseHasCommandsFalseEmpty() {
	all := makeTestPhaseHooks()
	assert.False(s.T(), phaseHasCommands(all, PhaseOnCreate))
}

func (s *LifecycleHookTestSuite) TestPhaseHasCommandsTrueRunFunc() {
	all := []phaseHook{
		{
			phase:   PhasePostCreate,
			runFunc: func() error { return nil },
		},
	}
	assert.True(s.T(), phaseHasCommands(all, PhasePostCreate))
}

func (s *LifecycleHookTestSuite) TestWaitForEmptyPhaseLogsWarning() {
	t := s.T()
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	all := makeTestPhaseHooks()
	phase := PhaseUpdateContent

	if !phaseHasCommands(all, phase) {
		log.Debugf(
			"waitFor phase %q has no commands configured; the split point is a no-op",
			phase,
		)
	}

	entries := logs.All()
	assert.NotEmpty(t, entries, "expected at least one log entry")
	assert.Contains(t, entries[0].Message,
		`waitFor phase "updateContentCommand" has no commands configured`)
}

func (s *LifecycleHookTestSuite) TestMergeSecretsEnv() {
	env := map[string]string{"EXISTING": "keep"}

	mergeSecretsEnv(env, []string{"SECRET_KEY=secret_val", "OTHER=data"})

	assert.Equal(s.T(), "keep", env["EXISTING"])
	assert.Equal(s.T(), "secret_val", env["SECRET_KEY"])
	assert.Equal(s.T(), "data", env["OTHER"])
}

func (s *LifecycleHookTestSuite) TestMergeSecretsEnvDoesNotOverride() {
	env := map[string]string{"MY_VAR": "original"}

	mergeSecretsEnv(env, []string{"MY_VAR=overridden"})

	assert.Equal(s.T(), "original", env["MY_VAR"])
}

func (s *LifecycleHookTestSuite) TestMergeSecretsEnvNil() {
	env := map[string]string{"KEY": "val"}

	mergeSecretsEnv(env, nil)

	assert.Equal(s.T(), "val", env["KEY"])
	assert.Len(s.T(), env, 1)
}

func (s *LifecycleHookTestSuite) TestMergeSecretsEnvValueWithEquals() {
	env := map[string]string{}

	mergeSecretsEnv(env, []string{"CONN=host=db port=5432"})

	assert.Equal(s.T(), "host=db port=5432", env["CONN"])
}

func (s *LifecycleHookTestSuite) TestPostAttachHooksRunEveryTime() {
	t := s.T()
	currentUser, err := user.Current()
	assert.NoError(t, err)

	dir := t.TempDir()
	counterFile := filepath.Join(dir, "counter.txt")

	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteUser: currentUser.Username,
			},
			UpdatedConfigProperties: config.UpdatedConfigProperties{
				PostAttachCommands: []types.LifecycleHook{
					{"": {"sh", "-c", fmt.Sprintf(
						`count=$(cat %s 2>/dev/null || echo 0); echo $((count+1)) > %s`,
						counterFile, counterFile,
					)}},
				},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			State: config.ContainerDetailsState{},
		},
		SubstitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: dir,
		},
	}

	// Run postAttachCommand multiple times — it must execute every time.
	for i := 1; i <= 3; i++ {
		err := RunPostAttachHooks(context.Background(), result, nil)
		assert.NoError(t, err)

		content, readErr := os.ReadFile(counterFile) //nolint:gosec // test file from TempDir
		assert.NoError(t, readErr)
		assert.Equal(t, fmt.Sprintf("%d\n", i), string(content),
			"postAttachCommand should run on call %d", i)
	}
}

func (s *LifecycleHookTestSuite) TestPostCreateHookUsesOnceSemantics() {
	t := s.T()

	// postCreateCommand passes container.Created as content to shouldSkipHook.
	// When content is non-empty, shouldSkipHook uses a marker file to ensure
	// the hook runs only once per container creation.
	skip, err := shouldSkipHook("test-postCreate", "")
	assert.NoError(t, err)
	assert.False(t, skip, "empty content should never skip")

	// Verify that preAttachPhaseParams sets content for postCreate and postStart.
	env := lifecycleEnv{remoteUser: "test", workspaceFolder: "/tmp"}
	result := &config.Result{
		MergedConfig: &config.MergedDevContainerConfig{
			UpdatedConfigProperties: config.UpdatedConfigProperties{
				PostCreateCommands: []types.LifecycleHook{{"": {"echo", "hi"}}},
				PostStartCommands:  []types.LifecycleHook{{"": {"echo", "hi"}}},
			},
		},
		ContainerDetails: &config.ContainerDetails{
			Created: "2024-01-01T00:00:00Z",
			State:   config.ContainerDetailsState{StartedAt: "2024-01-01T00:00:01Z"},
		},
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: "/tmp"},
	}

	hooks := preAttachPhaseParams(result, env, false)

	// postCreateCommand should have content = Created (non-empty → once semantics).
	var postCreate, postStart hookRunParams
	for _, h := range hooks {
		if h.phase == PhasePostCreate {
			postCreate = h.params
		}
		if h.phase == PhasePostStart {
			postStart = h.params
		}
	}
	assert.NotEmpty(t, postCreate.content,
		"postCreateCommand must have non-empty content for once-semantics")
	assert.NotEmpty(t, postStart.content,
		"postStartCommand must have non-empty content for once-semantics")
}

func (s *LifecycleHookTestSuite) TestPostAttachHookHasNoOnceGuard() {
	t := s.T()

	// RunPostAttachHooks passes content="" which means shouldSkipHook always
	// returns false — the hook runs every time. Verify the contract.
	skip, err := shouldSkipHook("postAttachCommands", "")
	assert.NoError(t, err)
	assert.False(t, skip, "postAttachCommand must never be skipped (content is always empty)")

	// Call shouldSkipHook multiple times with empty content — must never skip.
	for i := range 5 {
		skip, err := shouldSkipHook("postAttachCommands", "")
		assert.NoError(t, err)
		assert.False(t, skip, "postAttachCommand must not skip on call %d", i)
	}
}

func TestLifecycleHookTestSuite(t *testing.T) {
	suite.Run(t, new(LifecycleHookTestSuite))
}

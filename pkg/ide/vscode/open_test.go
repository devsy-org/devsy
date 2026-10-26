package vscode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type call struct {
	path string
	args []string
}

func (c call) isListExtensions() bool {
	return len(c.args) == 1 && c.args[0] == listExtensionsArg
}

type OpenSuite struct {
	suite.Suite
	logs     *observer.ObservedLogs
	calls    []call
	uris     []string
	launcher hostLauncher
	runFn    func(ctx context.Context, c call) ([]byte, error)
	uriErr   error
}

func TestOpenSuite(t *testing.T) {
	suite.Run(t, new(OpenSuite))
}

func (s *OpenSuite) SetupTest() {
	s.logs = log.InitTestObserved(s.T(), zapcore.DebugLevel)
	s.calls = nil
	s.uris = nil
	s.uriErr = nil
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return []byte("jeanp413.open-remote-ssh\n"), nil
		}
		return nil, nil
	}
	s.launcher = hostLauncher{
		lookPath: func(file string) (string, error) { return "/bin/" + file, nil },
		run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
			c := call{path: path, args: args}
			s.calls = append(s.calls, c)
			return s.runFn(ctx, c)
		},
		openURI: func(uri string) error {
			s.uris = append(s.uris, uri)
			return s.uriErr
		},
		goos: "linux",
	}
}

func testParams() OpenParams {
	return OpenParams{Workspace: "ws", Folder: "/workspaces/ws", Flavor: FlavorCodium}
}

func (s *OpenSuite) TestCLISuccessDispatchesOnceWithCorrelation() {
	operationID, err := s.openInOperation(testParams())

	s.Require().NoError(err)
	s.Require().NotEmpty(operationID)
	s.Len(s.calls, 2)
	s.Empty(s.uris)
	dispatched := s.records(zapcore.InfoLevel)
	s.Require().Len(dispatched, 1)
	s.Equal(operationID, dispatched[0].ContextMap()["operation"])
	s.Equal(string(FlavorCodium), dispatched[0].ContextMap()["flavor"])
	s.Equal(routeCLI, dispatched[0].ContextMap()["route"])
	resolved := s.logs.FilterField(zap.String("cli_kind", "no extension")).All()
	s.Require().Len(resolved, 1)
	s.Equal(stageResolve, resolved[0].ContextMap()["stage"])
}

func (s *OpenSuite) TestCLIFailureFallsBackAndKeepsCauseAndCorrelation() {
	cliCause := errors.New("exit status 1")
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return []byte("jeanp413.open-remote-ssh\n"), nil
		}
		return nil, cliCause
	}

	operationID, err := s.openInOperation(testParams())

	s.Require().NoError(err)
	s.Require().Len(s.uris, 1)
	s.Contains(s.uris[0], "codium://vscode-remote/")

	failed := s.records(zapcore.WarnLevel)
	s.Require().Len(failed, 1)
	s.Equal(operationID, failed[0].ContextMap()["operation"])
	s.Equal(routeCLI, failed[0].ContextMap()["route"])
	s.Equal(stageCLIDispatch, failed[0].ContextMap()["stage"])

	dispatched := s.records(zapcore.InfoLevel)
	s.Require().Len(dispatched, 1)
	s.Equal(operationID, dispatched[0].ContextMap()["operation"])
	s.Equal(routeURI, dispatched[0].ContextMap()["route"])
	s.Equal(true, dispatched[0].ContextMap()["after_cli_failure"])
}

func (s *OpenSuite) TestBothRoutesFailingKeepsCausesAndLogsBothFailures() {
	cliCause := errors.New("cli cause")
	uriCause := errors.New("uri cause")
	s.runFn = func(context.Context, call) ([]byte, error) { return nil, cliCause }
	s.uriErr = uriCause

	operationID, err := s.openInOperation(testParams())

	s.Require().Error(err)
	s.ErrorIs(err, cliCause)
	s.ErrorIs(err, uriCause)
	failed := s.records(zapcore.WarnLevel)
	s.Require().Len(failed, 2)
	for _, entry := range failed {
		s.Equal(operationID, entry.ContextMap()["operation"])
	}
	s.Equal(stageListExtensions, failed[0].ContextMap()["stage"])
	s.Equal(stageURIDispatch, failed[1].ContextMap()["stage"])
	s.Empty(s.records(zapcore.InfoLevel))
}

func (s *OpenSuite) TestCancelledCLIDoesNotOpenURI() {
	ctx, cancel := context.WithCancel(context.Background())
	s.runFn = func(context.Context, call) ([]byte, error) {
		cancel()
		return nil, context.Canceled
	}

	err := s.launcher.open(ctx, testParams())

	s.ErrorIs(err, context.Canceled)
	s.Empty(s.uris)
}

func (s *OpenSuite) TestMissingCLIFallsBackToURI() {
	s.launcher.lookPath = func(string) (string, error) { return "", errors.New("not found") }

	_, err := s.openInOperation(testParams())

	s.Require().NoError(err)
	s.Empty(s.calls)
	s.Len(s.uris, 1)
	failed := s.records(zapcore.WarnLevel)
	s.Require().Len(failed, 1)
	s.Equal(stageResolve, failed[0].ContextMap()["stage"])
}

func (s *OpenSuite) TestMissingSSHExtensionIsInstalledBeforeDispatch() {
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return []byte("ms-vscode-remote.remote-containers\n"), nil
		}
		return nil, nil
	}

	s.Require().NoError(s.launcher.open(context.Background(), testParams()))

	s.Require().Len(s.calls, 3)
	s.Equal([]string{"--install-extension", "jeanp413.open-remote-ssh"}, s.calls[1].args)
	s.Contains(s.calls[2].args, "--disable-extension")
}

func (s *OpenSuite) TestInstallFailureNamesStageAndKeepsCause() {
	installErr := errors.New("install cause")
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return nil, nil
		}
		return nil, installErr
	}

	err := s.launcher.openViaCLI(context.Background(), testParams(), nil)

	s.ErrorIs(err, installErr)
	s.Equal(stageInstallSSHExt, stageOf(err))
	s.Len(s.calls, 2)
}

// Editor output, paths and error text can hold secrets that no exact-value
// redactor knows about, so none of it may reach logs or rendered errors.
func (s *OpenSuite) TestLogsAndErrorsOmitEditorOutputPathsAndErrorText() {
	canaries := []string{
		`C:\Users\Alice\AppData\Local\Programs\VSCodium\bin\codium.cmd`,
		`c:/users/alice/appdata/local/programs/vscodium/bin/codium.cmd`,
		`\\fileserver\share\alice\profile`,
		`/home/alice/.config/VSCodium`,
		"build-host.internal",
		"token=unknown-query-token-8841",
		"boundary-secret-value-7731",
	}
	s.T().Setenv("DEVSY_TEST_TOKEN", canaries[6])
	// Exercise the old truncation boundary and check the leaked prefix too.
	output := strings.Repeat("x", 2040) + canaries[6] + strings.Repeat("y", 100)
	leaky := fmt.Errorf("%s: %s", strings.Join(canaries, " "), output)
	for _, failedStage := range []string{
		stageResolve, stageListExtensions, stageInstallSSHExt, stageCLIDispatch,
	} {
		s.Run(failedStage, func() {
			s.logs.TakeAll()
			s.launcher.lookPath = func(string) (string, error) {
				if failedStage == stageResolve {
					return "", leaky
				}
				return canaries[0], nil
			}
			s.runFn = func(_ context.Context, c call) ([]byte, error) {
				if c.isListExtensions() && failedStage != stageListExtensions {
					if failedStage == stageInstallSSHExt {
						return nil, nil
					}
					return []byte("jeanp413.open-remote-ssh\n"), nil
				}
				return []byte(output), leaky
			}
			s.uriErr = leaky
			params := testParams()
			params.Workspace = canaries[4]
			params.Folder = canaries[1]
			_, err := s.openInOperation(params)
			s.Require().Error(err)
			logged := s.everythingLogged() + err.Error()
			for _, canary := range canaries {
				s.NotContains(logged, canary)
			}
			s.NotContains(logged, canaries[6][:8])
			s.NotContains(logged, "Alice")
			s.NotContains(logged, "alice")
			s.ErrorIs(err, leaky)
			failed := s.records(zapcore.WarnLevel)
			s.Require().Len(failed, 2)
			s.Equal(failedStage, failed[0].ContextMap()["stage"])
			s.Equal(stageURIDispatch, failed[1].ContextMap()["stage"])
		})
	}
}

func (s *OpenSuite) TestLogsOmitResolvedPathOnSuccess() {
	const resolved = `C:\Users\Alice\AppData\Local\Programs\VSCodium\bin\codium.cmd`
	s.launcher.lookPath = func(string) (string, error) { return resolved, nil }

	s.Require().NoError(s.launcher.open(context.Background(), testParams()))

	s.NotContains(s.everythingLogged(), "Alice")
	s.NotContains(s.everythingLogged(), "alice")
}

func (s *OpenSuite) TestClassifyFailureUsesErrorKindsNotText() {
	s.Equal("canceled", classifyFailure(fmt.Errorf("x: %w", context.Canceled)))
	s.Equal("timeout", classifyFailure(fmt.Errorf("x: %w", context.DeadlineExceeded)))
	s.Equal("error", classifyFailure(errors.New("anything else")))

	_, lookErr := exec.LookPath("devsy-test-binary-that-does-not-exist")
	s.Equal("not found", classifyFailure(lookErr))
}

func (s *OpenSuite) TestClassifyFailureReportsExitStatus() {
	if runtime.GOOS == "windows" {
		s.T().Skip("requires a POSIX shell")
	}
	exitErr := exec.Command("sh", "-c", "exit 3").Run()

	s.Equal("exit status 3", classifyFailure(exitErr))
}

func (s *OpenSuite) TestCLIKindClassifiesByExtensionOnly() {
	s.Equal("batch wrapper", cliKind(`C:\Program Files\VSCodium\bin\codium.CMD`))
	s.Equal("native executable", cliKind(`C:\VSCodium\VSCodium.exe`))
	s.Equal("no extension", cliKind("/usr/bin/codium"))
}

func (s *OpenSuite) TestMacAppPathIsCandidateOnlyOnDarwin() {
	var looked []string
	s.launcher.lookPath = func(file string) (string, error) {
		looked = append(looked, file)
		return "", errors.New("not found")
	}
	config := openConfigs[FlavorCodium]

	_, err := s.launcher.resolveCLI(config, nil)
	s.Require().Error(err)
	s.Equal([]string{"codium"}, looked)

	looked = nil
	s.launcher.goos = "darwin"
	_, err = s.launcher.resolveCLI(config, nil)
	s.Require().Error(err)
	s.Equal([]string{"codium", config.macAppPath}, looked)
}

func (s *OpenSuite) TestHostCLIRejectsUntrustedCommands() {
	sh, err := exec.LookPath("sh")
	if err != nil {
		s.T().Skip("requires a POSIX shell")
	}
	marker := filepath.Join(s.T().TempDir(), "executed")
	_, err = runHostCLI(context.Background(), sh, "-c", "touch \"$1\"", "sh", marker)
	s.Require().Error(err)
	_, statErr := os.Stat(marker)
	s.ErrorIs(statErr, os.ErrNotExist)

	for _, args := range [][]string{
		{"--install-extension", "untrusted.extension"},
		{"--user-data-dir", marker},
		{"--reuse-window", "--folder-uri", "https://example.invalid/"},
		{"--reuse-window", "--folder-uri", "vscode-remote://ssh-remote+ws.devsy/w", "--extra"},
	} {
		_, err = runHostCLI(context.Background(), filepath.Join(s.T().TempDir(), "codium"), args...)
		s.Require().Error(err)
		s.NotErrorIs(err, os.ErrNotExist)
	}
}

func (s *OpenSuite) TestHostCLIEnumeratesAllExtensionsAndPreservesFolderArgv() {
	if runtime.GOOS == "windows" {
		s.T().Skip("fixture is a POSIX shell script, not Windows wrapper execution")
	}
	dir := filepath.Join(s.T().TempDir(), "editor space \u00e9")
	s.Require().NoError(os.MkdirAll(dir, 0o700))
	script := `#!/bin/sh
if [ "$1" = "--list-extensions" ]; then
  printf '%s\n' jeanp413.open-remote-ssh ms-vscode-remote.remote-containers
  i=0
  while [ "$i" -lt 260 ]; do
    printf 'test.extension-%s\n' "$i"
    i=$((i+1))
  done
  exit 0
fi
if [ "$1" = "--install-extension" ]; then exit 99; fi
printf '%s\n' "$@" > "$DEVSY_TEST_ARGV_FILE"
`
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "codium"), []byte(script), 0o700))
	argvFile := filepath.Join(dir, "argv")
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.T().Setenv("DEVSY_TEST_ARGV_FILE", argvFile)
	s.launcher = newHostLauncher()
	s.launcher.openURI = func(string) error { return errors.New("unexpected URI fallback") }
	params := testParams()
	params.Folder = "/workspaces/a b/\u96ea"
	params.NewWindow = true
	_, err := s.openInOperation(params)
	s.Require().NoError(err)
	argv, err := os.ReadFile(argvFile)
	s.Require().NoError(err)
	s.Equal("--disable-extension\nms-vscode-remote.remote-containers\n--new-window\n"+
		"--folder-uri\nvscode-remote://ssh-remote+ws.devsy/workspaces/a%20b/%E9%9B%AA\n", string(argv))
	s.Empty(s.records(zapcore.WarnLevel))
}

// openInOperation runs the launcher inside a real status operation, the way
// launchIDEPhase does, and returns the operation ID the launch was run under.
func (s *OpenSuite) openInOperation(params OpenParams) (operationID string, err error) {
	err = status.Run(
		context.Background(),
		status.Nop(),
		status.Operation{Phase: status.PhaseLaunchingIDE},
		func(ctx context.Context) error {
			operationID = status.OperationID(ctx)
			return s.launcher.open(ctx, params)
		},
	)
	s.Require().NotEmpty(operationID)
	for _, entry := range s.logs.AllUntimed() {
		s.Equal(operationID, entry.ContextMap()["operation"])
		s.Equal(string(params.Flavor), entry.ContextMap()["flavor"])
	}
	return operationID, err
}

func (s *OpenSuite) records(level zapcore.Level) []observer.LoggedEntry {
	return s.logs.FilterLevelExact(level).AllUntimed()
}

// everythingLogged renders every message and structured field of every entry,
// at every level, so privacy assertions cannot miss a field.
func (s *OpenSuite) everythingLogged() string {
	var b strings.Builder
	for _, entry := range s.logs.AllUntimed() {
		b.WriteString(entry.Message + "\n")
		for key, value := range entry.ContextMap() {
			fmt.Fprintf(&b, "%s=%v\n", key, value)
		}
	}
	return b.String()
}

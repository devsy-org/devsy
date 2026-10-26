package vscode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type call struct {
	path string
	args []string
}

func (c call) isListExtensions() bool {
	return len(c.args) == 1 && c.args[0] == "--list-extensions"
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

func messages(logs *observer.ObservedLogs, level zapcore.Level) string {
	var b strings.Builder
	for _, entry := range logs.FilterLevelExact(level).All() {
		b.WriteString(entry.Message + "\n")
	}
	return b.String()
}

func (s *OpenSuite) TestCLISuccessDoesNotUseURI() {
	s.Require().NoError(s.launcher.open(context.Background(), testParams()))

	s.Len(s.calls, 2)
	s.Equal("/bin/codium", s.calls[1].path)
	s.Empty(s.uris)
	s.Contains(
		messages(s.logs, zapcore.InfoLevel),
		"launch request dispatched to VSCodium through its CLI",
	)
}

func (s *OpenSuite) TestCLIFailureFallsBackAndKeepsDiagnostic() {
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return []byte("jeanp413.open-remote-ssh\n"), nil
		}
		return []byte("wrapper exploded"), errors.New("exit status 1")
	}

	s.Require().NoError(s.launcher.open(context.Background(), testParams()))

	s.Require().Len(s.uris, 1)
	s.Contains(s.uris[0], "codium://vscode-remote/")
	warning := messages(s.logs, zapcore.WarnLevel)
	s.Contains(warning, stageCLIDispatch)
	s.Contains(warning, "wrapper exploded")
	s.Contains(messages(s.logs, zapcore.InfoLevel), "through its URI handler after CLI failure")
}

func (s *OpenSuite) TestBothRouteFailuresAreJoined() {
	cliCause := errors.New("exit status 1")
	uriCause := errors.New("no handler")
	s.runFn = func(context.Context, call) ([]byte, error) { return nil, cliCause }
	s.uriErr = uriCause

	err := s.launcher.open(context.Background(), testParams())

	s.Require().Error(err)
	s.ErrorIs(err, cliCause)
	s.ErrorIs(err, uriCause)
	s.Contains(err.Error(), stageListExtensions)
	s.Contains(err.Error(), stageURIDispatch)
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

	s.Require().NoError(s.launcher.open(context.Background(), testParams()))

	s.Empty(s.calls)
	s.Len(s.uris, 1)
	s.Contains(messages(s.logs, zapcore.WarnLevel), stageResolve)
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

func (s *OpenSuite) TestInstallFailureNamesStage() {
	installErr := errors.New("exit status 1")
	s.runFn = func(_ context.Context, c call) ([]byte, error) {
		if c.isListExtensions() {
			return nil, nil
		}
		return nil, installErr
	}

	err := s.launcher.openViaCLI(context.Background(), testParams())

	s.ErrorIs(err, installErr)
	s.Contains(err.Error(), stageInstallSSHExt)
	s.Len(s.calls, 2)
}

func (s *OpenSuite) TestErrorOutputIsBoundedAndHomeIsMasked() {
	s.launcher.homeDir = "/home/alice"
	s.runFn = func(context.Context, call) ([]byte, error) {
		out := "failed in /home/alice/.vscode " + strings.Repeat("x", 3*maxLoggedOutputBytes)
		return []byte(out), errors.New("exit status 1")
	}

	err := s.launcher.openViaCLI(context.Background(), testParams())

	s.Require().Error(err)
	s.NotContains(err.Error(), "/home/alice")
	s.Contains(err.Error(), "~/.vscode")
	s.Contains(err.Error(), "[truncated]")
	s.Less(len(err.Error()), 2*maxLoggedOutputBytes)
}

func (s *OpenSuite) TestCLIKindDistinguishesWindowsWrappers() {
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

	_, err := s.launcher.resolveCLI(FlavorCodium, config)
	s.Require().Error(err)
	s.Equal([]string{"codium"}, looked)

	looked = nil
	s.launcher.goos = "darwin"
	_, err = s.launcher.resolveCLI(FlavorCodium, config)
	s.Require().Error(err)
	s.Equal([]string{"codium", config.macAppPath}, looked)
}

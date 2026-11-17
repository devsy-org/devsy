package clientimplementation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

const (
	binSh                = "/bin/sh"
	testProxyWorkspaceID = "workspace"
)

func TestHandleForceError(t *testing.T) {
	sentinel := errors.New("boom")

	tests := []struct {
		name    string
		err     error
		force   bool
		wantErr error
	}{
		{name: "no error", err: nil, force: false, wantErr: nil},
		{name: "no error forced", err: nil, force: true, wantErr: nil},
		{name: "error not forced", err: sentinel, force: false, wantErr: sentinel},
		{name: "error forced", err: sentinel, force: true, wantErr: nil},
		{
			name:    "deadline forced",
			err:     context.DeadlineExceeded,
			force:   true,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantErr, handleForceError(tt.err, tt.force))
		})
	}
}

func TestDeleteContext(t *testing.T) {
	s := &workspaceClient{}

	t.Run("empty grace period has no deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "")
		defer cancel()
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})

	t.Run("invalid grace period has no deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "not-a-duration")
		defer cancel()
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})

	t.Run("valid grace period sets deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "50ms")
		defer cancel()
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, time.Now().Add(50*time.Millisecond), deadline, time.Second)
	})
}

func TestRemoveAll(t *testing.T) {
	t.Run("missing path returns nil", func(t *testing.T) {
		assert.NoError(t, removeAll(filepath.Join(t.TempDir(), "does-not-exist")))
	})

	t.Run("existing tree is removed", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "tree")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o750))

		require.NoError(t, removeAll(dir))
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestRunCommand(t *testing.T) {
	t.Run("empty command is a no-op", func(t *testing.T) {
		assert.NoError(t, RunCommand(context.Background(), RunCommandOptions{}))
	})

	t.Run("multi-arg command runs via exec", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		err := RunCommand(context.Background(),
			RunCommandOptions{
				Command: types.StrArray{"echo", "hello"},
				Stdout:  stdout,
			})
		require.NoError(t, err)
		assert.Equal(t, "hello", strings.TrimSpace(stdout.String()))
	})

	t.Run("single command runs via emulated shell", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		err := RunCommand(context.Background(), RunCommandOptions{
			Command: types.StrArray{"echo emulated"},
			Stdout:  stdout,
		})
		require.NoError(t, err)
		assert.Equal(t, "emulated", strings.TrimSpace(stdout.String()))
	})

	t.Run("redacts environment secrets from both streams", func(t *testing.T) {
		const secret = "run-command-secret-846307" //nolint:gosec // test credential fixture
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		err := RunCommand(context.Background(), RunCommandOptions{
			Command: types.StrArray{
				binSh,
				"-c",
				`printf '%s' "$DEVSY_RUN_COMMAND_SECRET"; printf '%s' "$DEVSY_RUN_COMMAND_SECRET" >&2`,
			},
			Environ: []string{"DEVSY_RUN_COMMAND_SECRET=" + secret},
			Stdout:  stdout,
			Stderr:  stderr,
		})
		require.NoError(t, err)
		assert.NotContains(t, stdout.String(), secret)
		assert.NotContains(t, stderr.String(), secret)
		assert.Contains(t, stdout.String(), "***")
		assert.Contains(t, stderr.String(), "***")
	})
}

func TestRunCommandPreservesCancellationAndExit(t *testing.T) {
	requirePOSIXShell(t)
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunCommand(ctx, RunCommandOptions{
			Command: types.StrArray{binSh, "-c", `printf x > "$READY"; exec /bin/sleep 60`},
			Environ: []string{"READY=" + ready},
			Stdout:  io.Discard,
			Stderr:  io.Discard,
		})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
}

func TestMachineCommandErrorsRedactOpaquePayloadAndArguments(t *testing.T) {
	requirePOSIXShell(t)
	const payload = "private-command-payload-796412"
	const argument = "private-configured-argument-796412"
	for _, tc := range []struct {
		name    string
		command types.StrArray
	}{
		{
			name: "emulated shell",
			command: types.StrArray{
				`: '` + argument + `'; printf '%s' "$DEVSY_RUN_COMMAND_SECRET" >&2; exit 7`,
			},
		},
		{
			name:    "argv",
			command: types.StrArray{binSh, "-c", `printf '%s' "$DEVSY_RUN_COMMAND_SECRET" >&2; exit 7`, argument},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEVSY_RUN_COMMAND_SECRET", "credential-sentinel-61395")
			cfg := &provider.ProviderConfig{
				Name: "redaction-test",
				Exec: provider.ProviderCommands{
					Create:  types.StrArray{testProviderCreateCommand},
					Command: tc.command,
				},
			}
			devsy := &config.Config{
				DefaultContext: testLocksContext,
				Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
			}
			mc, err := NewMachineClient(
				devsy,
				cfg,
				&provider.Machine{ID: "machine", Context: testLocksContext},
			)
			require.NoError(t, err)
			var stderr bytes.Buffer
			err = mc.Command(
				context.Background(),
				client.CommandOptions{Command: payload, Stderr: &stderr},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "run provider command:")
			assert.NotContains(t, err.Error(), payload)
			assert.NotContains(t, err.Error(), argument)
			assert.NotContains(t, stderr.String(), payload)
			assert.NotContains(t, stderr.String(), argument)
			assert.Contains(t, stderr.String(), "***")
			if tc.name == "emulated shell" {
				var status interp.ExitStatus
				require.ErrorAs(t, err, &status)
				assert.Equal(t, 7, int(status))
			} else {
				var status *exec.ExitError
				require.ErrorAs(t, err, &status)
				assert.Equal(t, 7, status.ExitCode())
			}
		})
	}
}

func TestRunCommand_RawStdoutDoesNotHoldProtocolBytes(t *testing.T) {
	requireProtocolByteRoundTrip(
		t,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return RunCommand(ctx, RunCommandOptions{
				Command:   types.StrArray{binSh, "-c", "printf h; read ack"},
				Environ:   []string{"DEVSY_RUN_COMMAND_SECRET=hidden"},
				Stdin:     stdin,
				Stdout:    stdout,
				RawStdout: true,
			})
		},
	)
}

func TestRunCommand_RawStdoutStillRedactsStderr(t *testing.T) {
	requirePOSIXShell(t)
	const secret = "protocol-test-secret-846307" //nolint:gosec // test credential fixture
	var stdout, stderr bytes.Buffer
	err := RunCommand(context.Background(), RunCommandOptions{
		Command: types.StrArray{
			binSh,
			"-c",
			`printf '%s' "$DEVSY_RUN_COMMAND_SECRET"; printf '%s' "$DEVSY_RUN_COMMAND_SECRET" >&2`,
		},
		Environ:   []string{"DEVSY_RUN_COMMAND_SECRET=" + secret},
		Stdout:    &stdout,
		Stderr:    &stderr,
		RawStdout: true,
	})
	require.NoError(t, err)
	assert.Equal(t, secret, stdout.String())
	assert.NotContains(t, stderr.String(), secret)
	assert.Contains(t, stderr.String(), "***")
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRunCommand_ReportsFlushFailureWithoutReplacingCommandError(t *testing.T) {
	requirePOSIXShell(t)
	const secret = "hidden-flush-secret-846307" //nolint:gosec // test credential fixture
	flushErr := errors.New("flush failed")
	tests := []struct {
		name       string
		command    string
		stdout     io.Writer
		stderr     io.Writer
		commandErr bool
	}{
		{
			name:    "stdout flush",
			command: "printf h",
			stdout:  failingWriter{flushErr},
		},
		{
			name:    "stderr flush",
			command: "printf h >&2",
			stderr:  failingWriter{flushErr},
		},
		{
			name:       "command failure takes precedence",
			command:    "printf h; exit 7",
			stdout:     failingWriter{flushErr},
			commandErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunCommand(context.Background(), RunCommandOptions{
				Command: types.StrArray{binSh, "-c", tt.command},
				Environ: []string{"DEVSY_RUN_COMMAND_SECRET=" + secret},
				Stdout:  tt.stdout,
				Stderr:  tt.stderr,
			})
			if tt.commandErr {
				require.ErrorContains(t, err, "exit status 7")
				assert.NotErrorIs(t, err, flushErr)
				return
			}
			require.ErrorIs(t, err, flushErr)
		})
	}
}

func TestProxyUp_PreservesProtocolStdout(t *testing.T) {
	const contextName = "default"
	proxy := &proxyClient{
		devsyConfig: &config.Config{
			DefaultContext: contextName,
			Contexts: map[string]*config.ContextConfig{
				contextName: {},
			},
		},
		config: &provider.ProviderConfig{
			Name: "test",
			Exec: provider.ProviderCommands{
				Proxy: &provider.ProxyCommands{
					Up: types.StrArray{binSh, "-c", "printf h; read ack"},
				},
			},
		},
		workspace: &provider.Workspace{ID: "test", Context: contextName},
	}
	proxy.executor = &proxyExecutor{client: proxy}
	requireProtocolByteRoundTrip(
		t,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return proxy.Up(ctx, client.UpOptions{Stdin: stdin, Stdout: stdout})
		},
	)
}

func requireProtocolByteRoundTrip(
	t *testing.T,
	run func(context.Context, io.Reader, io.Writer) error,
) {
	t.Helper()
	requirePOSIXShell(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	defer func() { _ = stdinReader.Close() }()
	defer func() { _ = stdinWriter.Close() }()
	defer func() { _ = stdoutReader.Close() }()
	defer func() { _ = stdoutWriter.Close() }()
	stopClosingPipes := context.AfterFunc(ctx, func() {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	})
	defer stopClosingPipes()

	done := make(chan error, 1)
	go func() {
		defer func() { _ = stdoutWriter.Close() }()
		done <- run(ctx, stdinReader, stdoutWriter)
	}()

	got := make([]byte, 1)
	_, err := io.ReadFull(stdoutReader, got)
	require.NoError(t, err)
	require.Equal(t, []byte("h"), got)
	_, err = io.WriteString(stdinWriter, "ack\n")
	require.NoError(t, err)
	require.NoError(t, stdinWriter.Close())
	require.NoError(t, <-done)
}

func requirePOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
}

func TestLogBusy(t *testing.T) {
	t.Run("within threshold keeps timestamp", func(t *testing.T) {
		start := time.Now()
		logBusy(&start)
		assert.WithinDuration(t, time.Now(), start, time.Second)
	})

	t.Run("past threshold resets timestamp", func(t *testing.T) {
		start := time.Now().Add(-2 * logThreshold)
		logBusy(&start)
		assert.WithinDuration(t, time.Now(), start, time.Second)
	})
}

func TestStartWaitStatusHandlers(t *testing.T) {
	ctx := context.Background()

	t.Run("stopped without create errors", func(t *testing.T) {
		err := handleStoppedStatus(ctx, &fakeWorkspaceClient{}, false)
		assert.EqualError(t, err, "workspace is stopped")
	})

	t.Run("stopped with create starts workspace", func(t *testing.T) {
		fake := &fakeWorkspaceClient{}
		require.NoError(t, handleStoppedStatus(ctx, fake, true))
		assert.True(t, fake.startCalled)
	})

	t.Run("stopped start failure is wrapped", func(t *testing.T) {
		fake := &fakeWorkspaceClient{startErr: errors.New("nope")}
		err := handleStoppedStatus(ctx, fake, true)
		assert.ErrorContains(t, err, "start workspace")
	})

	t.Run("not found without create errors", func(t *testing.T) {
		err := handleNotFoundStatus(ctx, &fakeWorkspaceClient{}, false)
		assert.EqualError(t, err, "workspace not found")
	})

	t.Run("not found with create creates workspace", func(t *testing.T) {
		fake := &fakeWorkspaceClient{}
		require.NoError(t, handleNotFoundStatus(ctx, fake, true))
		assert.True(t, fake.createCalled)
	})
}

func TestStartWaitReturnsWhenRunning(t *testing.T) {
	fake := &fakeWorkspaceClient{status: client.StatusRunning}
	require.NoError(t, StartWait(context.Background(), fake, false))
}

func TestStartWaitPropagatesStatusError(t *testing.T) {
	fake := &fakeWorkspaceClient{statusErr: errors.New("status failed")}
	assert.ErrorContains(t, StartWait(context.Background(), fake, false), "status failed")
}

type fakeWorkspaceClient struct {
	client.WorkspaceClient

	status    client.Status
	statusErr error

	startCalled  bool
	startErr     error
	createCalled bool
	createErr    error
}

func (f *fakeWorkspaceClient) Status(
	context.Context,
	client.StatusOptions,
) (client.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeWorkspaceClient) Start(context.Context) error {
	f.startCalled = true
	return f.startErr
}

func (f *fakeWorkspaceClient) Create(context.Context) error {
	f.createCalled = true
	return f.createErr
}

func TestTelemetryProviderKindUsesBuiltinProvenance(t *testing.T) {
	const dockerProvider = "docker"
	const customProvider = "custom"
	for _, tc := range []struct {
		name   string
		config *provider.ProviderConfig
		want   string
	}{
		{"builtin alias", &provider.ProviderConfig{
			Name: "my-provider", Source: provider.ProviderSource{Internal: true, Raw: dockerProvider},
		}, dockerProvider},
		{"external with builtin alias", &provider.ProviderConfig{
			Name: dockerProvider, Source: provider.ProviderSource{Github: "example/provider"},
		}, customProvider},
		{"unknown internal", &provider.ProviderConfig{
			Source: provider.ProviderSource{Internal: true, Raw: "private-provider"},
		}, customProvider},
		{"missing config", nil, customProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &workspaceClient{providerConfig: tc.config}
			assert.Equal(t, tc.want, c.TelemetryProviderKind())
		})
	}
}

func TestProxyExecutionRecordsProviderMetric(t *testing.T) {
	requirePOSIXShell(t)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	const contextName = "default"
	proxy := &proxyClient{
		devsyConfig: &config.Config{
			DefaultContext: contextName,
			Contexts:       map[string]*config.ContextConfig{contextName: {}},
		},
		config:    &provider.ProviderConfig{Name: "proxy"},
		workspace: &provider.Workspace{ID: "private-workspace", Context: contextName},
	}
	executor := &proxyExecutor{client: proxy}
	metrics := telemetry.NewMetricsReporter()
	ctx := telemetry.WithMetrics(context.Background(), metrics)
	err := executor.execute(ctx, execParams{
		operation: "command",
		command:   types.StrArray{binSh, "-c", testShellExitSuccess},
		stdout:    io.Discard,
		stderr:    io.Discard,
	})
	require.NoError(t, err)
	snapshot := metrics.Finish(nil)
	require.Len(t, snapshot.ProviderOperations, 1)
	assert.Equal(t, telemetry.ProviderTypeScriptProxy, snapshot.ProviderOperations[0].ProviderType)
	assert.Equal(t, "command", snapshot.ProviderOperations[0].Operation)
	assert.EqualValues(t, 1, snapshot.ProviderOperations[0].Count)
}

func TestProxyUpForwardsRequestedLogLevel(t *testing.T) {
	requirePOSIXShell(t)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	log.InitTestObserved(t, log.SlogLevelDebug)
	const contextName = "default"
	for _, tc := range []struct{ level, want string }{
		{"warn", "warn"}, {"info", "info"}, {"trace", "trace"}, {"", "debug"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			var stdout bytes.Buffer
			proxy := &proxyClient{
				devsyConfig: &config.Config{
					DefaultContext: contextName,
					Contexts:       map[string]*config.ContextConfig{contextName: {}},
				},
				config: &provider.ProviderConfig{
					Name: "proxy",
					Exec: provider.ProviderCommands{Proxy: &provider.ProxyCommands{
						Up: types.StrArray{
							binSh,
							"-c",
							`printf '%s|%s|%s|%s' "$DEVSY_LOG_LEVEL" "$DEVSY_LOG_FORMAT" "$#" "$1"`,
							"provider",
							"sentinel",
						},
					}},
				},
				workspace: &provider.Workspace{ID: testProxyWorkspaceID, Context: contextName},
			}
			proxy.executor = &proxyExecutor{client: proxy}
			require.NoError(
				t,
				proxy.Up(
					context.Background(),
					client.UpOptions{LogLevel: tc.level, Stdout: &stdout},
				),
			)
			assert.Equal(t, tc.want+"|json|1|sentinel", stdout.String())
		})
	}
}

func TestProxyUpRejectsInvalidLogLevelBeforeExecution(t *testing.T) {
	proxy := &proxyClient{}
	err := proxy.Up(context.Background(), client.UpOptions{LogLevel: "invalid"})
	require.ErrorContains(t, err, "invalid log level")
}

func TestRunCommandLoggingEnvironmentDefaultsAndPrecedence(t *testing.T) {
	requirePOSIXShell(t)
	t.Setenv(config.EnvLogLevel, "warn")
	t.Setenv("DEVSY_LOG_FORMAT", "text")
	log.InitTestObserved(t, log.SlogLevelInfo)
	const printEnvironment = `printf '%s|%s' "$DEVSY_LOG_LEVEL" "$DEVSY_LOG_FORMAT"`
	for _, tc := range []struct {
		name    string
		env     []string
		command types.StrArray
		want    string
	}{
		{"inherited environment", nil, types.StrArray{binSh, "-c", printEnvironment}, "warn|text"},
		{"custom argv defaults", []string{}, types.StrArray{binSh, "-c", printEnvironment}, "info|json"},
		{"custom shell defaults", []string{}, types.StrArray{printEnvironment}, "info|json"},
		{
			"explicit format",
			[]string{"DEVSY_LOG_FORMAT=plain"},
			types.StrArray{binSh, "-c", printEnvironment},
			"info|plain",
		},
		{"last explicit values win", []string{
			"DEVSY_LOG_LEVEL=trace", "DEVSY_LOG_LEVEL=warn", "DEVSY_LOG_FORMAT=json", "DEVSY_LOG_FORMAT=logfmt",
		}, types.StrArray{binSh, "-c", printEnvironment}, "warn|logfmt"},
		{"shell last explicit values win", []string{
			"DEVSY_LOG_LEVEL=trace", "DEVSY_LOG_LEVEL=warn", "DEVSY_LOG_FORMAT=json", "DEVSY_LOG_FORMAT=logfmt",
		}, types.StrArray{printEnvironment}, "warn|logfmt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := RunCommand(context.Background(), RunCommandOptions{
				Command: tc.command,
				Environ: tc.env,
				Stdout:  &stdout,
				Stderr:  io.Discard,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, stdout.String())
		})
	}
}

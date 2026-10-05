package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"al.essio.dev/pkg/shellescape"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	execerr "k8s.io/client-go/util/exec"
)

const testVersion = "v1.2.3"

// recordingExec records each call, replays stdouts[N] as stdout, and returns
// errs[N] on call N (nil when unset).
type recordingExec struct {
	calls   []recordedCall
	stdouts []string
	errs    []error
}

type recordedCall struct {
	argv  []string
	stdin string
}

func (r *recordingExec) fn(_ context.Context, argv []string, streams driver.Streams) error {
	call := recordedCall{argv: argv}
	if streams.Stdin != nil {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, streams.Stdin)
		call.stdin = buf.String()
	}
	idx := len(r.calls)
	r.calls = append(r.calls, call)
	if streams.Stdout != nil && idx < len(r.stdouts) {
		_, _ = io.WriteString(streams.Stdout, r.stdouts[idx])
	}
	if idx < len(r.errs) {
		return r.errs[idx]
	}
	return nil
}

func binarySourceFrom(data string) BinarySourceFunc {
	return func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(data)), nil
	}
}

func TestKubernetesDelivery_Phase(t *testing.T) {
	d := &KubernetesDelivery{}
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestKubernetesDelivery_DeliverPreStart_ReturnsError(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support pre-start")
}

func TestKubernetesDelivery_DeliverPostStart_RequiresBinarySource(t *testing.T) {
	exec := &recordingExec{}
	d := &KubernetesDelivery{Exec: exec.fn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary source is required")
}

func TestKubernetesDelivery_DeliverPostStart_RequiresExec(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: fakeBinarySource,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec function is required")
}

func TestKubernetesDelivery_DeliverPostStart_WritesBinary(t *testing.T) {
	binaryData := "test-binary-content"
	exec := &recordingExec{stdouts: []string{""}}

	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom(binaryData),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 3)
	destPath := pkgconfig.ContainerDevsyHelperLocation

	probeScript := strings.Join(exec.calls[0].argv, " ")
	assert.Contains(t, probeScript, "--version")
	assert.Contains(t, probeScript, destPath)

	writeScript := strings.Join(exec.calls[1].argv, " ")
	assert.Contains(t, writeScript, ".devsy-transfer-")
	assert.NotContains(t, writeScript, "chmod 0755")
	assert.NotContains(t, writeScript, "mv -f")
	assert.Contains(t, writeScript, path.Dir(destPath))
	assert.Equal(t, binaryData, exec.calls[1].stdin)
	commitScript := strings.Join(exec.calls[2].argv, " ")
	assert.Contains(t, commitScript, "--version")
	assert.Contains(t, commitScript, testVersion)
	assert.Contains(t, commitScript, "mv -f")
	assert.Contains(t, commitScript, destPath)
}

func TestKubernetesDelivery_DeliverPostStart_SkipsWhenVersionMatches(t *testing.T) {
	exec := &recordingExec{stdouts: []string{testVersion + "\n"}}

	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom("should-not-be-streamed"),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 1, "only the version probe should run")
	assert.Empty(t, exec.calls[0].stdin)
}

func TestKubernetesDelivery_DeliverPostStart_DeliversWhenProbeErrors(t *testing.T) {
	probeErr := &recordingExec{
		stdouts: []string{""},
		errs:    []error{fmt.Errorf("probe boom"), nil},
	}
	d := &KubernetesDelivery{Exec: probeErr.fn, ExpectedVersion: testVersion}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom("data"),
		Arch:         testArch,
	})
	require.NoError(t, err)
	assert.Len(t, probeErr.calls, 3)
}

func TestKubernetesDelivery_DeliverPostStart_BinarySourceError(t *testing.T) {
	exec := &recordingExec{stdouts: []string{""}}
	d := &KubernetesDelivery{Exec: exec.fn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nil, fmt.Errorf("download failed")
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquire binary")
}

func TestKubernetesDelivery_Cleanup_IsNoOp(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.Cleanup(context.Background(), "workspace-123")
	assert.NoError(t, err)
}

func TestKubernetesDelivery_DeliverPostStart_PrefersDownloadOverExecStream(t *testing.T) {
	exec := &recordingExec{stdouts: []string{""}}
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource:              binarySourceFrom("should-not-be-streamed"),
		Arch:                      testArch,
		DownloadURL:               "https://example.com/releases",
		PreferInContainerDownload: true,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 2, "probe, then in-container download")
	downloadScript := strings.Join(exec.calls[1].argv, " ")
	assert.Contains(t, downloadScript, "curl")
	assert.Contains(t, downloadScript, "example.com/releases")
	assert.Empty(t, exec.calls[1].stdin, "download must not receive the binary over stdin")
}

func TestKubernetesDelivery_DeliverPostStart_FallsBackToExecStreamWhenNoDownloadTool(t *testing.T) {
	binaryData := "test-binary-content"
	exec := &recordingExec{
		stdouts: []string{""},
		errs: []error{
			nil,
			execerr.CodeExitError{
				Code: noDownloadToolExitCode,
				Err:  fmt.Errorf("command terminated with exit code %d", noDownloadToolExitCode),
			},
		},
	}
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource:              binarySourceFrom(binaryData),
		Arch:                      testArch,
		DownloadURL:               "https://example.com/releases",
		PreferInContainerDownload: true,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 4, "probe, failed download, stage, then validation and commit")
	assert.Equal(t, binaryData, exec.calls[2].stdin)
}

func TestKubernetesDelivery_DeliverPostStart_SkipsDownloadWhenNoURLConfigured(t *testing.T) {
	binaryData := "test-binary-content"
	exec := &recordingExec{stdouts: []string{""}}
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom(binaryData),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 3, "probe, stage, then validation and commit")
	assert.Equal(t, binaryData, exec.calls[1].stdin)
}

func TestKubernetesDelivery_DeliverViaExecStream_RetriesTransientFailureOnce(t *testing.T) {
	exec := &recordingExec{
		errs: []error{
			fmt.Errorf("write binary to container: %w", context.DeadlineExceeded),
			nil, // cleanup
			nil, // retry stage
			nil, // retry validation and commit
		},
	}
	d := &KubernetesDelivery{Exec: exec.fn}

	err := d.deliverViaExecStream(context.Background(), "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: binarySourceFrom("data"),
		Arch:         testArch,
	})
	require.NoError(t, err)
	assert.Len(t, exec.calls, 4, "failed stage, cleanup, retry stage, and commit")
	firstTemp := stagedPath(t, exec.calls[0])
	secondTemp := stagedPath(t, exec.calls[2])
	assert.NotEqual(t, firstTemp, secondTemp, "every retry must use an independent staging path")
}

func TestKubernetesDelivery_DeliverViaExecStream_DoesNotRetryPermanentFailure(t *testing.T) {
	permanentErr := execerr.CodeExitError{Code: 1, Err: fmt.Errorf("no such file or directory")}
	exec := &recordingExec{errs: []error{permanentErr}}
	d := &KubernetesDelivery{Exec: exec.fn}

	err := d.deliverViaExecStream(context.Background(), "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: binarySourceFrom("data"),
		Arch:         testArch,
	})
	require.Error(t, err)
	assert.Len(t, exec.calls, 2, "a permanent failure is cleaned up but not retried")
}

func TestKubernetesDelivery_InterruptedStageNeverCommits(t *testing.T) {
	var calls []recordedCall
	attempt := 0
	d := &KubernetesDelivery{ExpectedVersion: testVersion}
	d.Exec = func(_ context.Context, argv []string, streams driver.Streams) error {
		call := recordedCall{argv: argv}
		if streams.Stdin != nil {
			attempt++
			if attempt == 1 {
				buf := make([]byte, 4)
				n, _ := streams.Stdin.Read(buf)
				call.stdin = string(buf[:n])
			} else {
				data, err := io.ReadAll(streams.Stdin)
				require.NoError(t, err)
				call.stdin = string(data)
			}
		}
		calls = append(calls, call)
		if streams.Stdin != nil && attempt == 1 {
			return fmt.Errorf("stream interrupted: unexpected EOF")
		}
		return nil
	}

	err := d.deliverViaExecStream(context.Background(), "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: binarySourceFrom("complete-binary"),
		Arch:         testArch,
	})
	require.NoError(t, err)
	require.Len(t, calls, 4)
	assert.NotContains(t, strings.Join(calls[0].argv, " "), "mv -f")
	assert.Contains(t, strings.Join(calls[1].argv, " "), "rm -f")
	assert.NotEqual(t, stagedPath(t, calls[0]), stagedPath(t, calls[2]))
	assert.Contains(t, strings.Join(calls[3].argv, " "), "mv -f")
}

func TestKubernetesDelivery_ValidationFailureCleansUpWithoutPromotion(t *testing.T) {
	exec := &recordingExec{
		errs: []error{
			nil,
			execerr.CodeExitError{Code: 1, Err: fmt.Errorf("wrong version")},
			nil,
		},
	}
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}

	err := d.deliverViaExecStream(context.Background(), "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: binarySourceFrom("invalid-binary"),
		Arch:         testArch,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate staged agent")
	require.Len(t, exec.calls, 3)
	assert.NotContains(t, strings.Join(exec.calls[0].argv, " "), "mv -f")
	assert.Contains(t, strings.Join(exec.calls[2].argv, " "), "rm -f")
}

type delayedReader struct {
	chunks int
	delay  time.Duration
}

func (r *delayedReader) Read(p []byte) (int, error) {
	if r.chunks == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	r.chunks--
	p[0] = 'x'
	return 1, nil
}

func TestKubernetesDelivery_LongProgressingTransferSucceeds(t *testing.T) {
	exec := &recordingExec{}
	d := &KubernetesDelivery{
		Exec:                  exec.fn,
		ExpectedVersion:       testVersion,
		execStreamIdleTimeout: 200 * time.Millisecond,
	}
	start := time.Now()
	err := d.execStreamOnce(context.Background(), "/usr/local/bin/devsy", &delayedReader{
		chunks: 12,
		delay:  30 * time.Millisecond,
	}, false)
	require.NoError(t, err)
	assert.Greater(t, time.Since(start), 300*time.Millisecond)
	assert.Len(t, exec.calls, 2)
}

func TestKubernetesDelivery_StalledTransferCancelsWithoutCommit(t *testing.T) {
	var calls []recordedCall
	d := &KubernetesDelivery{
		ExpectedVersion:       testVersion,
		execStreamIdleTimeout: 20 * time.Millisecond,
	}
	d.Exec = func(ctx context.Context, argv []string, streams driver.Streams) error {
		calls = append(calls, recordedCall{argv: argv})
		if streams.Stdin == nil {
			return nil
		}
		buf := make([]byte, 1)
		_, _ = streams.Stdin.Read(buf)
		<-ctx.Done()
		return ctx.Err()
	}

	err := d.execStreamOnce(
		context.Background(), "/usr/local/bin/devsy", strings.NewReader("x"), false,
	)
	require.ErrorIs(t, err, errExecStreamIdleTimeout)
	require.Len(t, calls, 2, "stage and cleanup only")
	assert.NotContains(t, strings.Join(calls[0].argv, " "), "mv -f")
	assert.Contains(t, strings.Join(calls[1].argv, " "), "rm -f")
}

func TestKubernetesDelivery_PreservesDownloadAndStreamErrors(t *testing.T) {
	exec := &recordingExec{errs: []error{
		nil,
		fmt.Errorf("egress blocked"),
		fmt.Errorf("stream broken"),
		nil,
	}}
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource:              binarySourceFrom("binary"),
		Arch:                      testArch,
		DownloadURL:               "https://example.com/releases",
		PreferInContainerDownload: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in-container download failed")
	assert.Contains(t, err.Error(), "egress blocked")
	assert.Contains(t, err.Error(), "exec-stream delivery failed")
	assert.Contains(t, err.Error(), "stream broken")
}

func TestIsTransientDeliveryError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not transient", err: nil, want: false},
		{name: "our own deadline firing is transient", err: context.DeadlineExceeded, want: true},
		{name: "i/o timeout is transient", err: fmt.Errorf("write tcp: i/o timeout"), want: true},
		{name: "broken pipe is transient", err: fmt.Errorf("write: broken pipe"), want: true},
		{
			name: "connection reset is transient",
			err:  fmt.Errorf("read: connection reset by peer"),
			want: true,
		},
		{
			name: "a real exit code is not transient",
			err:  execerr.CodeExitError{Code: 1, Err: fmt.Errorf("boom")},
			want: false,
		},
		{
			name: "missing download tool is not transient",
			err: execerr.CodeExitError{
				Code: noDownloadToolExitCode,
				Err:  fmt.Errorf("command terminated with exit code 127"),
			},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isTransientDeliveryError(c.err))
		})
	}
}

func TestKubernetesDelivery_DeliverPostStart_UsesInstallPathOverride(t *testing.T) {
	binaryData := "test-binary-content"
	exec := &recordingExec{stdouts: []string{""}}
	installPath := testKubernetesInstallPath
	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion, InstallPath: installPath}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom(binaryData),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 3)
	probeScript := strings.Join(exec.calls[0].argv, " ")
	assert.Contains(t, probeScript, installPath)
	writeScript := strings.Join(exec.calls[1].argv, " ")
	assert.Contains(t, writeScript, path.Dir(installPath))
	assert.NotContains(t, writeScript, pkgconfig.ContainerDevsyHelperLocation)
	assert.Contains(t, strings.Join(exec.calls[2].argv, " "), installPath)
}

func stagedPath(t *testing.T, call recordedCall) string {
	t.Helper()
	for field := range strings.FieldsSeq(strings.Join(call.argv, " ")) {
		field = strings.Trim(field, "';")
		if strings.Contains(field, ".devsy-transfer-") {
			return field
		}
	}
	t.Fatal("staging path not found")
	return ""
}

func TestKubernetesDelivery_DestPath_DefaultsWhenInstallPathUnset(t *testing.T) {
	d := &KubernetesDelivery{}
	assert.Equal(t, pkgconfig.ContainerDevsyHelperLocation, d.destPath())
}

func TestKubernetesDelivery_ExecStreamPreservesInstalledAgent(t *testing.T) {
	sourceErr := errors.New("binary source failed")
	for _, tc := range []struct {
		name         string
		version      string
		truncate     bool
		cancel       bool
		sourceError  bool
		incomplete   bool
		wantExecuted bool
		wantError    string
	}{
		{name: "complete transfer", version: testVersion, wantExecuted: true},
		{name: "truncated but executable", version: testVersion, truncate: true, wantError: "size mismatch"},
		{name: "wrong version", version: "v0.0.1", wantExecuted: true, wantError: "version mismatch"},
		{name: "cancelled stage", version: testVersion, cancel: true, wantError: "context canceled"},
		{name: "source read error", version: testVersion, sourceError: true, wantError: "binary source failed"},
		{name: "remote success before EOF", version: testVersion, incomplete: true, wantError: "incomplete agent stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest, marker := installedAgentFixture(t)
			dir := filepath.Dir(dest)
			binary := fmt.Sprintf(
				"#!/bin/sh\necho executed > %s\necho %s\n# trailing bytes\n",
				shellescape.Quote(marker),
				tc.version,
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := &KubernetesDelivery{ExpectedVersion: testVersion}
			var cancelStage func()
			if tc.cancel {
				cancelStage = cancel
			}
			d.Exec = shellAgentExec(tc.truncate, tc.incomplete, cancelStage)
			var reader io.Reader = strings.NewReader(binary)
			if tc.sourceError {
				reader = io.MultiReader(reader, iotest.ErrReader(sourceErr))
			}
			err := d.execStreamOnce(ctx, dest, reader, false)
			installed, readErr := os.ReadFile(dest) // #nosec G304 -- reads an owned test fixture
			require.NoError(t, readErr)
			if tc.wantError == "" {
				require.NoError(t, err)
				assert.Equal(t, binary, string(installed))
			} else {
				require.ErrorContains(t, err, tc.wantError)
				assert.Equal(t, "installed agent", string(installed))
			}
			assertAgentStagingClean(t, dir, marker, tc.wantExecuted)
		})
	}
}

func TestKubernetesDelivery_CustomAgentSkipsVersionChecks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		truncate bool
		badCLI   bool
		wantErr  string
	}{
		{name: "complete transfer replaces matching installed agent"},
		{name: "truncated transfer is rejected", truncate: true, wantErr: "size mismatch"},
		{name: "version command failure is rejected", badCLI: true, wantErr: "not executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest, marker := installedAgentFixture(t)
			installed := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo " + testVersion + "; exit 0; fi\n"
			require.NoError(t, os.WriteFile(dest, []byte(installed), 0o600))
			chmodErr := os.Chmod(dest, 0o700) // #nosec G302 -- owned executable fixture
			require.NoError(t, chmodErr)

			cliCommand := "echo custom-agent-version; exit 0"
			if tc.badCLI {
				cliCommand = "exit 1"
			}
			binary := fmt.Sprintf(
				"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then %s; fi\necho executed > %s\n# trailing bytes\n",
				cliCommand,
				shellescape.Quote(marker),
			)
			d := &KubernetesDelivery{
				Exec:            shellAgentExec(tc.truncate, false, nil),
				ExpectedVersion: testVersion,
				InstallPath:     dest,
			}
			err := d.DeliverPostStart(context.Background(), PostStartOptions{
				BinarySource:     binarySourceFrom(binary),
				Arch:             testArch,
				SkipVersionCheck: true,
			})

			actual, readErr := os.ReadFile(dest) // #nosec G304 -- reads an owned test fixture
			require.NoError(t, readErr)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, binary, string(actual))
			} else {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Equal(
					t,
					installed,
					string(actual),
					"failed custom delivery must preserve the installed agent",
				)
			}
			assertAgentStagingClean(t, filepath.Dir(dest), marker, false)
		})
	}
}

func TestKubernetesDelivery_ValidationIsBounded(t *testing.T) {
	calls := 0
	d := &KubernetesDelivery{
		ExpectedVersion:       testVersion,
		execStreamIdleTimeout: 50 * time.Millisecond,
	}
	d.Exec = func(ctx context.Context, _ []string, streams driver.Streams) error {
		calls++
		switch calls {
		case 1:
			_, err := io.Copy(io.Discard, streams.Stdin)
			return err
		case 2:
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			<-ctx.Done()
			return ctx.Err()
		default:
			require.NoError(t, ctx.Err(), "cleanup must have an independent context")
			return nil
		}
	}
	err := d.execStreamOnce(
		context.Background(),
		"/usr/local/bin/devsy",
		strings.NewReader("binary"),
		false,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 3, calls)
}

func shellAgentExec(truncate, incomplete bool, cancelStage func()) ArgvExecFunc {
	return func(ctx context.Context, argv []string, streams driver.Streams) error {
		if streams.Stdin != nil && incomplete {
			_, err := io.CopyN(io.Discard, streams.Stdin, 1)
			return err
		}
		if streams.Stdin != nil && truncate {
			data, err := io.ReadAll(streams.Stdin)
			if err != nil {
				return err
			}
			streams.Stdin = bytes.NewReader(data[:len(data)-len("# trailing bytes\n")])
		}
		cmd := exec.CommandContext(
			ctx,
			argv[0],
			argv[1:]...) // #nosec G204 -- executes delivery-generated scripts in a test directory
		cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
		err := cmd.Run()
		if streams.Stdin != nil && cancelStage != nil {
			cancelStage()
		}
		return err
	}
}

func installedAgentFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agent directory's")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	dest := filepath.Join(dir, "devsy")
	require.NoError(t, os.WriteFile(dest, []byte("installed agent"), 0o600))
	return dest, filepath.Join(dir, "executed")
}

func assertAgentStagingClean(t *testing.T, dir, marker string, wantExecuted bool) {
	t.Helper()
	_, err := os.Stat(marker)
	if wantExecuted {
		require.NoError(t, err)
	} else {
		require.ErrorIs(
			t,
			err,
			os.ErrNotExist,
			"invalid transfers must not execute the staged binary",
		)
	}
	staged, err := filepath.Glob(filepath.Join(dir, ".devsy-transfer-*"))
	require.NoError(t, err)
	assert.Empty(t, staged)
}

func TestKubernetesDelivery_RetriesValidationTimeout(t *testing.T) {
	recorder := &recordingExec{errs: []error{nil, context.DeadlineExceeded, nil, nil, nil}}
	d := &KubernetesDelivery{Exec: recorder.fn, ExpectedVersion: testVersion}
	err := d.deliverViaExecStream(context.Background(), "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: binarySourceFrom("binary"),
	})
	require.NoError(t, err)
	require.Len(t, recorder.calls, 5)
	assert.NotEqual(t, stagedPath(t, recorder.calls[0]), stagedPath(t, recorder.calls[3]))
}

func TestKubernetesDelivery_CancelledContextDoesNotAcquireBinary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &KubernetesDelivery{}
	err := d.deliverViaExecStream(ctx, "/usr/local/bin/devsy", PostStartOptions{
		BinarySource: func(context.Context, string) (io.ReadCloser, error) {
			t.Fatal("cancelled delivery must not acquire a binary")
			return nil, nil
		},
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestProgressReaderTimeoutBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	idleTimeout := time.Second
	completionTimeout := time.Minute
	for _, tc := range []struct {
		name         string
		lastProgress time.Time
		eofAt        time.Time
		want         error
	}{
		{name: "active source", lastProgress: now.Add(-idleTimeout / 2)},
		{name: "idle source", lastProgress: now.Add(-idleTimeout), want: errExecStreamIdleTimeout},
		{name: "draining after EOF", lastProgress: now.Add(-completionTimeout), eofAt: now.Add(-2 * idleTimeout)},
		{name: "completion deadline", eofAt: now.Add(-completionTimeout), want: errExecStreamCompletionTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &progressReader{lastProgress: tc.lastProgress, eofAt: tc.eofAt}
			err := reader.timeoutError(now, idleTimeout, completionTimeout)
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

func TestKubernetesDelivery_SlowCompletionAfterEOFSucceeds(t *testing.T) {
	calls := 0
	d := &KubernetesDelivery{
		ExpectedVersion:             testVersion,
		execStreamIdleTimeout:       20 * time.Millisecond,
		execStreamCompletionTimeout: time.Second,
	}
	d.Exec = func(ctx context.Context, _ []string, streams driver.Streams) error {
		calls++
		if streams.Stdin == nil {
			return nil
		}
		_, err := io.Copy(io.Discard, streams.Stdin)
		if err != nil {
			return err
		}
		timer := time.NewTimer(3 * d.idleTimeout())
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	err := d.execStreamOnce(
		context.Background(),
		"/usr/local/bin/devsy",
		strings.NewReader("binary"),
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "stage completion must be followed by validation and promotion")
}

func TestKubernetesDelivery_StalledCompletionAfterEOFCancels(t *testing.T) {
	var calls []recordedCall
	d := &KubernetesDelivery{
		ExpectedVersion:             testVersion,
		execStreamIdleTimeout:       10 * time.Millisecond,
		execStreamCompletionTimeout: 30 * time.Millisecond,
	}
	d.Exec = func(ctx context.Context, argv []string, streams driver.Streams) error {
		calls = append(calls, recordedCall{argv: argv})
		if streams.Stdin == nil {
			return nil
		}
		_, err := io.Copy(io.Discard, streams.Stdin)
		if err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	err := d.execStreamOnce(
		context.Background(),
		"/usr/local/bin/devsy",
		strings.NewReader("binary"),
		false,
	)
	require.ErrorIs(t, err, errExecStreamCompletionTimeout)
	assert.True(t, isTransientDeliveryError(err))
	require.Len(t, calls, 2, "stage and cleanup only")
	assert.NotContains(t, strings.Join(calls[0].argv, " "), "mv -f")
	assert.Contains(t, strings.Join(calls[1].argv, " "), "rm -f")
}

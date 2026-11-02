package clientimplementation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

func TestProviderCancellationHelper(t *testing.T) {
	if os.Getenv("DEVSY_CANCEL_HELPER") != "1" {
		return
	}
	marker := os.Getenv(config.EnvProviderCancelFile)
	switch os.Getenv("DEVSY_CANCEL_BEHAVIOR") {
	case "acknowledge":
		fmt.Fprint(os.Stdout, "ready")
		for {
			if _, err := os.Stat(marker); err == nil {
				if path := os.Getenv("DEVSY_CANCEL_ACK_PATH"); path != "" {
					_ = os.WriteFile(path, []byte("finished"), 0o600)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "ignore":
		fmt.Fprint(os.Stdout, "ready")
		time.Sleep(time.Minute)
	case "transport-read", "transport-write":
		ready := os.Getenv("DEVSY_CANCEL_READY_PATH")
		_ = os.WriteFile(ready, []byte("ready"), 0o600)
		go func() {
			for {
				if _, err := os.Stat(marker); err == nil {
					_ = os.WriteFile(os.Getenv("DEVSY_CANCEL_ACK_PATH"), []byte("finished"), 0o600)
					os.Exit(0)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		if os.Getenv("DEVSY_CANCEL_BEHAVIOR") == "transport-read" {
			var b [1]byte
			_, _ = os.Stdin.Read(b[:])
			for {
				time.Sleep(time.Second)
			}
		} else {
			if runtime.GOOS != "windows" {
				signal.Ignore(syscall.Signal(13))
			}
			chunk := bytes.Repeat([]byte("x"), 1024)
			for {
				if _, err := os.Stdout.Write(chunk); err != nil {
					for {
						time.Sleep(time.Second)
					}
				}
			}
		}
	case "status":
		os.Exit(7)
	case "status-127":
		os.Exit(127)
	case "bytes":
		os.Stdout.Write([]byte{0, 0xff, 0x80, '\r', '\n'})
		os.Stderr.Write([]byte(os.Getenv("DEVSY_CANCEL_SECRET")))
	case "environment":
		fmt.Fprint(os.Stdout, marker)
	default:
		os.Exit(125)
	}
	os.Exit(0)
}

func cancelHelperOptions(behavior string) RunCommandOptions {
	binary, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return RunCommandOptions{
		Command: types.StrArray{binary, "-test.run=^TestProviderCancellationHelper$"},
		Environ: append(
			os.Environ(),
			"DEVSY_CANCEL_HELPER=1",
			"DEVSY_CANCEL_BEHAVIOR="+behavior,
		),
		Cancellation: provider.ProviderCancellationFile,
	}
}

func TestProviderCancellationCooperatesAndCleansUp(t *testing.T) {
	ack := filepath.Join(t.TempDir(), "ack")
	opts := cancelHelperOptions("acknowledge")
	opts.Environ = append(opts.Environ, "DEVSY_CANCEL_ACK_PATH="+ack)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	opts.RawStdout = true
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not acknowledge cancellation")
	}
	contents, err := os.ReadFile(ack)
	require.NoError(t, err)
	require.Equal(t, "finished", string(contents))
}

type readyWriter struct{ ready chan struct{} }

func (w *readyWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("ready")) {
		select {
		case <-w.ready:
		default:
			close(w.ready)
		}
	}
	return len(p), nil
}

func TestProviderCancellationForcedAfterProductionGrace(t *testing.T) {
	opts := cancelHelperOptions("ignore")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	opts.RawStdout = true
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.GreaterOrEqual(t, time.Since(start), providerCancellationGrace)
	case <-time.After(providerCancellationGrace + 8*time.Second):
		t.Fatal("owned provider process was not reaped")
	}
}

func TestProviderCancellationPrelaunchAndMarkerFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller stopped")
	cancel(cause)
	require.ErrorIs(t, RunCommand(ctx, cancelHelperOptions("ignore")), cause)

	dir := t.TempDir()
	marker := filepath.Join(dir, "cancel")
	require.NoError(t, os.Mkdir(marker, 0o700))
	err := publishProviderCancellation(marker)
	require.ErrorContains(t, err, "not a regular file")
	require.NotContains(t, err.Error(), dir)

	require.NoError(t, os.Remove(marker))
	require.NoError(t, publishProviderCancellation(marker))
	require.NoError(t, publishProviderCancellation(marker))
	require.NoError(t, os.Remove(marker))
	if err := os.Symlink(filepath.Join(dir, "missing"), marker); err == nil {
		require.ErrorContains(t, publishProviderCancellation(marker), "not a regular file")
	}
}

func TestProviderCancellationPublicationFailureReapsProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := cancelHelperOptions("ignore")
	opts.RawStdout = true
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	markerPath := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- runProviderWithCancellationUsingPublisher(ctx, opts, func(marker string) error {
			markerPath <- marker
			return &os.PathError{Op: "open", Path: marker, Err: os.ErrPermission}
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, os.ErrPermission)
		marker := <-markerPath
		require.NotContains(t, err.Error(), marker)
		_, statErr := os.Stat(filepath.Dir(marker))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not stop after publication failure")
	}
}

func TestProviderCancellationPrivateEnvironmentAndTypedExits(t *testing.T) {
	dir, err := createProviderInvocationDir()
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	require.True(t, filepath.IsAbs(dir))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}

	opts := cancelHelperOptions("environment")
	opts.Environ = append(
		opts.Environ,
		config.EnvProviderCancelFile+"=forged",
		config.EnvProviderCancelFile+"=again",
	)
	if runtime.GOOS == "windows" {
		opts.Environ = append(
			opts.Environ,
			strings.ToLower(config.EnvProviderCancelFile)+"=windows-forged",
		)
	}
	var stdout bytes.Buffer
	opts.Stdout = &stdout
	require.NoError(t, RunCommand(context.Background(), opts))
	marker := stdout.String()
	require.NotContains(t, marker, "forged")
	require.True(t, filepath.IsAbs(marker))
	_, err = os.Stat(filepath.Dir(marker))
	require.ErrorIs(t, err, os.ErrNotExist)

	for _, status := range []int{7, 125, 127} {
		opts = cancelHelperOptions("status")
		if status == 125 {
			opts.Environ = append(opts.Environ, "DEVSY_CANCEL_BEHAVIOR=other")
		}
		if status == 127 {
			opts.Environ = append(opts.Environ, "DEVSY_CANCEL_BEHAVIOR=status-127")
		}
		err = RunCommand(context.Background(), opts)
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, status, exitErr.ExitCode())
	}

	if runtime.GOOS != "windows" {
		binary, err := os.Executable()
		require.NoError(t, err)
		for _, status := range []int{7, 127} {
			command := "command-that-does-not-exist-846307"
			if status == 7 {
				command = fmt.Sprintf(
					"DEVSY_CANCEL_HELPER=1 DEVSY_CANCEL_BEHAVIOR=status '%s' -test.run=^TestProviderCancellationHelper$",
					binary,
				)
			}
			err := RunCommand(context.Background(), RunCommandOptions{
				Command: types.StrArray{
					command,
				},
				Environ:      os.Environ(),
				Cancellation: provider.ProviderCancellationFile,
			})
			var shellExit interp.ExitStatus
			require.ErrorAs(t, err, &shellExit)
			require.Equal(t, status, int(shellExit))
		}
	}
}

func TestProviderCancellationRawBytesAndStderrRedaction(t *testing.T) {
	opts := cancelHelperOptions("bytes")
	secret := "provider-cancel-secret-84392"
	opts.Environ = append(opts.Environ, "DEVSY_CANCEL_SECRET="+secret)
	opts.RawStdout = true
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	require.NoError(t, RunCommand(context.Background(), opts))
	require.Equal(t, []byte{0, 0xff, 0x80, '\r', '\n'}, stdout.Bytes())
	require.NotContains(t, stderr.String(), secret)
	require.Contains(t, stderr.String(), "***")
}

func TestMachineCommandTransportCancellationJoinsCallback(t *testing.T) {
	for _, behavior := range []string{"transport-read", "transport-write"} {
		t.Run(behavior, func(t *testing.T) {
			dir := t.TempDir()
			ready, ack := filepath.Join(dir, "ready"), filepath.Join(dir, "ack")
			t.Setenv("DEVSY_CANCEL_HELPER", "1")
			t.Setenv("DEVSY_CANCEL_BEHAVIOR", behavior)
			t.Setenv("DEVSY_CANCEL_READY_PATH", ready)
			t.Setenv("DEVSY_CANCEL_ACK_PATH", ack)
			machine := &provider.Machine{ID: "cancel-machine", Context: testLocksContext}
			cfg := &provider.ProviderConfig{
				Name: "cancel-provider",
				Exec: provider.ProviderCommands{
					Create: types.StrArray{
						"create",
					},
					Command:      cancelHelperOptions(behavior).Command,
					Cancellation: provider.ProviderCancellationFile,
				},
			}
			devsy := &config.Config{
				DefaultContext: testLocksContext,
				Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
			}
			mc, err := NewMachineClient(devsy, cfg, machine)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := mc.OpenCommandTransport(
				ctx,
				client.CommandTransportOptions{Command: "opaque"},
			)
			require.NoError(t, err)
			defer conn.Close()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transport provider did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			completed := make(chan error, 1)
			go func() { completed <- conn.Wait() }()
			select {
			case err := <-completed:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("transport callback did not finish")
			}
			contents, err := os.ReadFile(ack)
			require.NoError(t, err)
			require.Equal(t, "finished", string(contents))
		})
	}
}

func TestWorkspaceCommandCancellationJoinsProviderCleanup(t *testing.T) {
	for _, transportMode := range []bool{false, true} {
		t.Run(fmt.Sprint("transport=", transportMode), func(t *testing.T) {
			ack := filepath.Join(t.TempDir(), "ack")
			t.Setenv("DEVSY_CANCEL_HELPER", "1")
			t.Setenv("DEVSY_CANCEL_BEHAVIOR", "acknowledge")
			t.Setenv("DEVSY_CANCEL_ACK_PATH", ack)
			workspace := &provider.Workspace{
				ID:      "cancel-workspace",
				Context: testLocksContext,
				Provider: provider.WorkspaceProviderConfig{
					Name: "cancel-provider",
				},
			}
			cfg := &provider.ProviderConfig{
				Name: "cancel-provider",
				Exec: provider.ProviderCommands{
					Command:      cancelHelperOptions("acknowledge").Command,
					Cancellation: provider.ProviderCancellationFile,
				},
			}
			devsy := &config.Config{
				DefaultContext: testLocksContext,
				Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
			}
			wc := &workspaceClient{
				providerWorkspace: workspace,
				providerConfig:    cfg,
				devsyConfig:       devsy,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if transportMode {
				conn, err := wc.OpenCommandTransport(ctx, client.CommandTransportOptions{Command: "opaque"})
				require.NoError(t, err)
				defer conn.Close()
				ready := make([]byte, 5)
				require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
				_, err = io.ReadFull(conn, ready)
				require.NoError(t, err)
				require.Equal(t, "ready", string(ready))
				cancel()
				require.ErrorIs(t, conn.Wait(), context.Canceled)
			} else {
				ready := make(chan struct{})
				finished := make(chan error, 1)
				go func() {
					finished <- wc.Command(ctx, client.CommandOptions{
						Command: "opaque",
						Stdout:  &readyWriter{ready: ready},
					})
				}()
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("workspace provider did not start")
				}
				cancel()
				select {
				case err := <-finished:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(5 * time.Second):
					t.Fatal("workspace provider did not finish")
				}
			}
			contents, err := os.ReadFile(ack)
			require.NoError(t, err)
			require.Equal(t, "finished", string(contents))
		})
	}
}

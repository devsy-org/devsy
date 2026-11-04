package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

func TestProviderExecHelper(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_EXEC_HELPER") != "1" {
		return
	}
	if os.Getenv("DEVSY_PROVIDER_EXEC_EXIT") == "wait" {
		if _, err := fmt.Fprint(os.Stdout, "ready"); err != nil {
			os.Exit(126)
		}
		time.Sleep(time.Minute)
	}
	if os.Getenv("DEVSY_PROVIDER_EXEC_EXIT") == "read-eof" {
		providerExecReadToEOF()
	}
	if os.Getenv("DEVSY_PROVIDER_EXEC_EXIT") != "" {
		os.Exit(125)
	}
	result := struct {
		Directory string
		Present   string
		Missing   string
		Argument  string
	}{Present: os.Getenv("PRESENT"), Missing: os.Getenv("MISSING")}
	result.Directory, _ = os.Getwd()
	result.Argument = os.Args[len(os.Args)-1]
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func providerExecReadToEOF() {
	if _, err := fmt.Fprint(os.Stdout, "before"); err != nil {
		os.Exit(126)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(126)
	}
	if _, err := fmt.Fprintf(os.Stdout, "after%s", input); err != nil {
		os.Exit(126)
	}
	os.Exit(0)
}

func TestProviderExecHandlerExportsUnsetAndWorkingDirectory(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	workdir := t.TempDir()
	command := fmt.Sprintf(
		"export PRESENT='quoted value'; unset MISSING; cd %s; %s -test.run=^TestProviderExecHelper$ 'quoted argument'",
		strconv.Quote(workdir),
		strconv.Quote(binary),
	)
	if runtime.GOOS == providerWindowsOS {
		command = strings.ReplaceAll(command, `\`, `/`)
	}
	var stdout, stderr bytes.Buffer
	err = RunEmulatedShell(context.Background(), &CommandRunner{
		Command: command, ExecHandler: ProviderExecHandler(),
		Environ: append(os.Environ(), "DEVSY_PROVIDER_EXEC_HELPER=1", "MISSING=should-be-unset"),
		Stdout:  &stdout, Stderr: &stderr,
	})
	require.NoErrorf(t, err, "stderr: %s", stderr.String())
	var got struct{ Directory, Present, Missing, Argument string }
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	expectedDir, err := filepath.EvalSymlinks(workdir)
	require.NoError(t, err)
	actualDir, err := filepath.EvalSymlinks(got.Directory)
	require.NoError(t, err)
	require.Truef(t, strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(expectedDir)),
		"working directory: got %q, want %q", got.Directory, workdir)
	require.Equal(t, "quoted value", got.Present)
	require.Empty(t, got.Missing)
	require.Equal(t, "quoted argument", got.Argument)
}

func TestProviderExecHandlerKeepsOutputAfterInputEOF(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	var stdout bytes.Buffer
	err = RunEmulatedShell(context.Background(), &CommandRunner{
		Command: strconv.Quote(binary) + " -test.run=^TestProviderExecHelper$",
		Environ: append(
			os.Environ(),
			"DEVSY_PROVIDER_EXEC_HELPER=1",
			"DEVSY_PROVIDER_EXEC_EXIT=read-eof",
		),
		ExecHandler: ProviderExecHandler(),
		Stdin:       strings.NewReader("payload"),
		Stdout:      &stdout,
	})
	require.NoError(t, err)
	require.Equal(t, "beforeafterpayload", stdout.String())
}

func TestProviderExecHandlerExitStatuses(t *testing.T) {
	for _, tc := range []struct {
		command string
		status  int
	}{
		{command: "command-that-does-not-exist-846307", status: 127},
		{command: "exit 7", status: 7},
		{command: "exit 125", status: 125},
	} {
		var stderr bytes.Buffer
		err := RunEmulatedShell(context.Background(), &CommandRunner{
			Command:     tc.command,
			ExecHandler: ProviderExecHandler(),
			Environ:     os.Environ(),
			Stderr:      &stderr,
		})
		exitStatus, ok := errors.AsType[interp.ExitStatus](err)
		if !ok || int(exitStatus) != tc.status {
			t.Errorf("%q: got %v, want exit %d", tc.command, err, tc.status)
		}
	}
}

func TestProviderExecHandlerCancellationReturnsCause(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("forced provider cancellation")
	ready := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- RunEmulatedShell(ctx, &CommandRunner{
			Command:     strconv.Quote(binary) + " -test.run=^TestProviderExecHelper$",
			Environ:     append(os.Environ(), "DEVSY_PROVIDER_EXEC_HELPER=1", "DEVSY_PROVIDER_EXEC_EXIT=wait"),
			ExecHandler: ProviderExecHandler(), Stdout: &shellReadyWriter{ready: ready},
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("shell child did not start")
	}
	cancel(cause)
	select {
	case err := <-finished:
		if !errors.Is(err, cause) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider shell did not finish")
	}
}

type shellReadyWriter struct{ ready chan struct{} }

func (w *shellReadyWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("ready")) {
		select {
		case <-w.ready:
		default:
			close(w.ready)
		}
	}
	return len(p), nil
}

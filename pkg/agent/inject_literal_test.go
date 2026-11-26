//nolint:gosec // These regressions execute generated shell checks and a private executable fixture.
package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/stretchr/testify/require"
)

func TestVersionCheckerLiteralPathAndVersion(t *testing.T) {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("POSIX /bin/sh is unavailable")
	}
	root := t.TempDir()
	agentPath := filepath.Join(root, "agent '\"\\\n雪 $(touch sentinel)`touch sentinel`")
	version := "v1 ' \" \\ 雪 $(touch sentinel)`touch sentinel`"
	literalVersion, err := inject.QuotePOSIXLiteral(version)
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(agentPath, []byte("#!/bin/sh\nprintf '%s\\n' "+literalVersion+"\n"), 0o700),
	)
	vc := &versionChecker{remoteVersion: version}
	predicate, err := vc.buildExistsCheck(agentPath)
	require.NoError(t, err)
	cmd := exec.CommandContext(
		t.Context(),
		"/bin/sh",
		"-c",
		"if "+predicate+"; then printf install; else printf reuse; fi",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "reuse", string(output))

	run := func(ctx context.Context, code string, stdin io.Reader, stdout, stderr io.Writer) error {
		command := exec.CommandContext(ctx, "/bin/sh", "-c", code)
		command.Dir = root
		command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
		return command.Run()
	}
	detected, err := vc.detectRemoteAgentVersion(t.Context(), run, agentPath)
	require.NoError(t, err)
	require.Equal(t, version, detected)
	_, err = os.Stat(filepath.Join(root, "sentinel"))
	require.True(t, os.IsNotExist(err))

	vc.remoteVersion = "mismatch"
	predicate, err = vc.buildExistsCheck(agentPath)
	require.NoError(t, err)
	require.NoError(t, exec.CommandContext(t.Context(), "/bin/sh", "-c", predicate).Run())
}

func TestVersionCheckerRejectsNULBeforeExecution(t *testing.T) {
	vc := &versionChecker{remoteVersion: "v1"}
	_, err := vc.buildExistsCheck("bad\x00path")
	require.Error(t, err)
	vc.remoteVersion = "v1\x00bad"
	_, err = vc.buildExistsCheck("/tmp/agent")
	require.Error(t, err)
	_, err = vc.detectRemoteAgentVersion(
		t.Context(),
		func(context.Context, string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("must not execute an invalid path")
			return nil
		},
		"bad\x00path",
	)
	require.Error(t, err)

	for _, field := range []string{"path", "version", "url", "command"} {
		opts := &InjectOptions{
			Exec: func(context.Context, string, io.Reader, io.Writer, io.Writer) error { return nil },
		}
		switch field {
		case "path":
			opts.RemoteAgentPath = "bad\x00path"
		case "version":
			opts.RemoteVersion = "bad\x00version"
		case "url":
			opts.DownloadURL = "bad\x00url"
		case "command":
			opts.Command = "bad\x00command"
		}
		require.Error(t, opts.Validate(), strings.ToUpper(field))
	}
}

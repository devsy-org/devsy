package framework

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	defaultCapture = "capture"
	defaultOutput  = "output"
)

func TestDefaultExecMasksLiveOutput(t *testing.T) {
	t.Setenv("DEVSY_FIXTURE_TOKEN", "synthetic-token-value-12345")
	for _, name := range []string{defaultCapture, defaultOutput, "containing", "stdout", formatJSON} {
		t.Run(name, func(t *testing.T) {
			mode := "default-mask"
			if name == formatJSON {
				mode = "json-mask"
			}
			t.Setenv("DEVSY_CAPTURE_CHILD", mode)
			executable, err := os.Executable()
			require.NoError(t, err)
			f := &Framework{
				DevsyBinDir:  filepath.Dir(executable),
				DevsyBinName: filepath.Base(executable),
			}
			outFile, err := os.CreateTemp(t.TempDir(), "stdout")
			require.NoError(t, err)
			defer func() { _ = outFile.Close() }()
			errFile, err := os.CreateTemp(t.TempDir(), "stderr")
			require.NoError(t, err)
			defer func() { _ = errFile.Close() }()
			oldOut, oldErr := os.Stdout, os.Stderr
			os.Stdout, os.Stderr = outFile, errFile
			defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
			raw := runDefaultMaskHelper(t, f, name)
			for _, file := range []*os.File{outFile, errFile} {
				data, err := os.ReadFile(file.Name())
				require.NoError(t, err)
				require.Contains(t, string(data), "public detail")
				require.NotContains(t, string(data), "CANARY")
				require.NotContains(t, string(data), "synthetic-token-value-12345")
			}
			if name == defaultCapture || name == defaultOutput {
				require.Contains(t, raw, "CANARY")
				require.Contains(t, raw, "synthetic-token-value-12345")
			}
		})
	}
}

func runDefaultMaskHelper(t *testing.T, f *Framework, name string) string {
	t.Helper()
	ctx := context.Background()
	args := []string{captureChildArg}
	switch name {
	case defaultCapture:
		out, stderr, err := f.ExecCommandCapture(ctx, args)
		require.NoError(t, err)
		require.Equal(t, out, stderr)
		return out
	case defaultOutput:
		out, err := f.ExecCommandOutput(ctx, args)
		require.NoError(t, err)
		return out
	case "containing":
		require.NoError(t, f.ExecCommandContaining(ctx, "CANARY", args))
	case "stdout":
		require.NoError(t, f.ExecCommandStdout(ctx, args))
	case formatJSON:
		result, err := f.ExecCommandJSON(ctx, args)
		require.NoError(t, err)
		require.Equal(t, "CANARY", result["Cookie"])
	}
	return ""
}

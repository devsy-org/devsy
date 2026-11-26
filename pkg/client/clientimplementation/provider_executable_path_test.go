package clientimplementation

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

func spacedProviderExecutable(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Jane Doe", "binaries")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	name := "provider"
	if runtime.GOOS == testWindowsOS {
		name += ".exe"
	}
	self, err := os.Executable()
	require.NoError(t, err)
	target := filepath.Join(dir, name)
	copyExecutable(t, self, target)
	return target
}

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	srcRoot, err := os.OpenRoot(filepath.Dir(source))
	require.NoError(t, err)
	defer func() { require.NoError(t, srcRoot.Close()) }()
	src, err := srcRoot.Open(filepath.Base(source))
	require.NoError(t, err)
	defer func() { require.NoError(t, src.Close()) }()
	dstRoot, err := os.OpenRoot(filepath.Dir(target))
	require.NoError(t, err)
	defer func() { require.NoError(t, dstRoot.Close()) }()
	dst, err := dstRoot.OpenFile(filepath.Base(target), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, err)
	require.NoError(t, dst.Chmod(0o700))
	require.NoError(t, dst.Close())
}

func runSpacedProvider(
	t *testing.T,
	cancellation provider.ProviderCancellationMode,
	binaries map[string][]*provider.ProviderBinary,
) (string, error) {
	t.Helper()
	executable := spacedProviderExecutable(t)
	var stdout, stderr bytes.Buffer
	err := RunCommand(context.Background(), RunCommandOptions{
		Command: types.StrArray{"${SSH_PROVIDER} init"},
		Environ: append(
			os.Environ(),
			"SSH_PROVIDER="+executable,
			providerExecutableHelperEnv+"=1",
		),
		ProviderBinaries: binaries,
		Stdout:           &stdout,
		Stderr:           &stderr,
		RawStdout:        true,
		Cancellation:     cancellation,
	})
	return stdout.String(), err
}

func TestProviderShellKeepsSpacedExecutablePathIntact(t *testing.T) {
	binaries := map[string][]*provider.ProviderBinary{"SSH_PROVIDER": {}}

	output, err := runSpacedProvider(t, "", binaries)

	require.NoError(t, err)
	require.Equal(t, "helper ran with init", output)
}

func TestSupervisedProviderShellKeepsSpacedExecutablePathIntact(t *testing.T) {
	binaries := map[string][]*provider.ProviderBinary{"SSH_PROVIDER": {}}

	output, err := runSpacedProvider(t, provider.ProviderCancellationFile, binaries)

	require.NoError(t, err)
	require.Equal(t, "helper ran with init", output)
}

func TestSupervisedProviderShellSplitsSpacedPathWithoutProviderBinaries(t *testing.T) {
	_, err := runSpacedProvider(t, provider.ProviderCancellationFile, nil)

	require.Error(t, err)
}

func TestProviderShellArgsCarryBinaryPathsInSortedOrder(t *testing.T) {
	files := &providerShellInvocation{script: "s", force: "f", ack: "a"}
	opts := RunCommandOptions{
		Environ: []string{"B_BIN=/b path/x", "A_BIN=/a", "OTHER=1"},
		ProviderBinaries: map[string][]*provider.ProviderBinary{
			"B_BIN": {}, "A_BIN": {},
		},
	}

	args := providerShellArgs("devsy", files, opts)

	require.Equal(t, []string{
		providerShellExecutablePathFlag, "A_BIN=/a",
		providerShellExecutablePathFlag, "B_BIN=/b path/x",
	}, args[len(args)-4:])
}

func TestProviderShellArgsOmitBinaryPathsWhenNoneConfigured(t *testing.T) {
	files := &providerShellInvocation{script: "s", force: "f", ack: "a"}

	args := providerShellArgs("devsy", files, RunCommandOptions{Environ: []string{"A=1"}})

	require.NotContains(t, args, providerShellExecutablePathFlag)
}

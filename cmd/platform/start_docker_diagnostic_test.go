package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

const platformFoldedValue = "synthetic-platform-continuation"

func TestDockerStartErrorMasksFoldedOutput(t *testing.T) {
	const fixture = "synthetic-docker-start-continuation"
	exit := &exec.ExitError{
		Stderr: []byte("Authorization: Bearer\r\n " + fixture + "\r\npublic stderr"),
	}
	err := WrapCommandError([]byte("public stdout"), exit)
	require.ErrorIs(t, err, exit)
	assert.NotContains(t, err.Error(), fixture)
	assert.Contains(t, err.Error(), "public stdout")
	assert.Contains(t, err.Error(), "public stderr")
}

func TestPlatformToolFailuresMaskFoldedOutput(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	fixturePlatformTools(t, dir)
	t.Setenv("PATH", dir)
	target := helmTarget{chartName: "fixture"}
	for _, fixture := range []struct {
		name string
		run  func() error
	}{
		{"docker", (&StartCmd{}).prepareDocker},
		{"helm version", func() error { return checkCLITools(ctx, "") }},
		{"kubectl version", func() error { return checkCLITools(ctx, "fixture") }},
		{"ingress", func() error { return installNginxIngress(ctx, nil, "") }},
		{"upgrade", func() error { return upgradeRelease(ctx, target, nil) }},
		{"manifests", func() error {
			_, err := getReleaseManifests(ctx, target, nil)
			return err
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Setenv("DEVSY_FIXTURE_TOOL_SUCCESS", fixture.name)
			err := fixture.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "public tool failure")
			assert.NotContains(t, err.Error(), platformFoldedValue)
		})
	}
}

func TestHelmUninstallFailureMasksLoggedOutput(t *testing.T) {
	fixturePlatformTools(t, t.TempDir())
	observed := log.InitTestObserved(t, zapcore.DebugLevel)
	runHelmUninstallCommand(platformToolCommand(t.Context(), "helm"))
	require.Positive(t, observed.FilterMessageSnippet("public tool failure").Len())
	require.Zero(t, observed.FilterMessageSnippet(platformFoldedValue).Len())
}

func fixturePlatformTools(t *testing.T, dir string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	original := platformToolCommand
	t.Cleanup(func() { platformToolCommand = original })
	platformToolCommand = func(ctx context.Context, name string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, self, "-test.run=^TestSyntheticPlatformTool$")
		command.Env = append(os.Environ(), "DEVSY_PLATFORM_DIAGNOSTIC_FIXTURE="+name)
		return command
	}
	for _, name := range []string{"docker", "helm", "kubectl"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		mode, err := os.Stat(path)
		require.NoError(t, err)
		require.NoError(t, os.Chmod(path, mode.Mode()|0o100))
	}
}

func TestSyntheticPlatformTool(t *testing.T) {
	tool := os.Getenv("DEVSY_PLATFORM_DIAGNOSTIC_FIXTURE")
	if tool == "" ||
		tool == "helm" && os.Getenv("DEVSY_FIXTURE_TOOL_SUCCESS") == "kubectl version" {
		return
	}
	_, _ = os.Stderr.WriteString(
		"public tool failure\nAuthorization: Bearer\n " + platformFoldedValue + "\n",
	)
	os.Exit(9)
}

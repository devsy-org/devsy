package delivery

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testArch = "amd64"

const (
	testSeedWorkspaceID = "ws1"
	testSeedVolumeName  = "ws1-workspace"
	testSeedSourceDir   = "/local/src"
)

func TestLocalDockerDelivery_Phase(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.Equal(t, PhasePreStart, d.Phase())
}

func TestLocalDockerDelivery_DeliverPreStart_RequiresBinarySource(t *testing.T) {
	d := &LocalDockerDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary source is required")
}

func TestLocalDockerDelivery_DeliverPostStart_ReturnsError(t *testing.T) {
	d := &LocalDockerDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support post-start")
}

func TestDeliveryPhase_String(t *testing.T) {
	assert.Equal(t, "pre-start", PhasePreStart.String())
	assert.Equal(t, "post-start", PhasePostStart.String())
	assert.Contains(t, DeliveryPhase(99).String(), "unknown")
}

func TestBinaryName(t *testing.T) {
	name := binaryName()
	assert.Equal(t, "devsy", name)
}

func TestLocalDockerDelivery_HelperImageName_Default(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.Equal(t, "busybox:latest", d.helperImageName())
}

func TestLocalDockerDelivery_HelperImageName_Configured(t *testing.T) {
	d := &LocalDockerDelivery{HelperImage: "registry.internal/tools/busybox:1.36"}
	assert.Equal(t, "registry.internal/tools/busybox:1.36", d.helperImageName())
}

func TestNewAgentDelivery_LocalDocker_ThreadsHelperImage(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: defaultDockerCmd,
		HelperImage:   "my-registry/busybox:1.35",
	}

	d := NewAgentDelivery(opts)
	local, ok := d.(*LocalDockerDelivery)
	require.True(t, ok)
	assert.Equal(t, "my-registry/busybox:1.35", local.HelperImage)
}

func TestNewAgentDelivery_LocalDocker_EmptyHelperImage(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: defaultDockerCmd,
	}

	d := NewAgentDelivery(opts)
	local, ok := d.(*LocalDockerDelivery)
	require.True(t, ok)
	assert.Empty(t, local.HelperImage)
	assert.Equal(t, "busybox:latest", local.helperImageName())
}

func TestPopulateVolume_FallbackToDirectCopy(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  run) echo \"image not found\" >&2; exit 1 ;;\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	binaryContent := []byte("fake-agent-binary-content")
	binarySource := func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(binaryContent)), nil
	}

	d := &LocalDockerDelivery{
		DockerCommand: scriptPath,
	}

	err := d.populateVolume(context.Background(), "test-vol", binarySource, testArch)
	require.NoError(t, err)

	destPath := filepath.Join(mountDir, binaryName())
	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestIsPodman(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "podman-rootful")
	require.NoError(t, os.WriteFile(
		wrapper,
		[]byte("#!/bin/sh\necho 'podman version 5.8.4'\n"),
		0o600,
	))
	// #nosec G302 -- test wrapper must be executable
	require.NoError(t, os.Chmod(wrapper, 0o700))
	d := &LocalDockerDelivery{DockerCommand: wrapper}
	assert.True(t, d.isPodman(), "wrapper names must be classified through runtime detection")
	d.Runtime = docker.RuntimeDocker
	assert.False(t, d.isPodman(), "explicit resolved runtime takes precedence")
}

func TestPodmanDeliveryUsesMountpointWithoutRunHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	logPath := filepath.Join(tmpDir, "calls")
	runtimePath := filepath.Join(tmpDir, "podman-rootful")
	runtimeScript := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"$1 $2\" = 'volume inspect' ]; then echo '" + mountDir + "'; exit 0; fi\n" +
		"if [ \"$1\" = 'unshare' ]; then shift; exec \"$@\"; fi\n" +
		"if [ \"$1\" = 'run' ]; then echo unexpected-helper >&2; exit 99; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(runtimePath, []byte(runtimeScript), 0o600))
	// #nosec G302 -- fake runtime must be executable
	require.NoError(t, os.Chmod(runtimePath, 0o700))

	agentPath := filepath.Join(mountDir, binaryName())
	require.NoError(t, os.WriteFile(agentPath, []byte("#!/bin/sh\necho v1.2.3\n"), 0o600))
	// #nosec G302 -- fake agent must be executable
	require.NoError(t, os.Chmod(agentPath, 0o700))
	d := &LocalDockerDelivery{DockerCommand: runtimePath, Runtime: docker.RuntimePodman}
	assert.Equal(t, "v1.2.3", d.detectVolumeVersion(context.Background(), "test-vol"))

	content := []byte("agent-binary")
	sourceCalls := 0
	source := func(context.Context, string) (io.ReadCloser, error) {
		sourceCalls++
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	require.NoError(t, d.populateVolume(context.Background(), "test-vol", source, testArch))
	got, err := os.ReadFile(agentPath) //nolint:gosec // temporary test mountpoint
	require.NoError(t, err)
	assert.Equal(t, content, got)
	info, err := os.Stat(agentPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	calls, err := os.ReadFile(logPath) //nolint:gosec // temporary test log
	require.NoError(t, err)
	assert.NotContains(t, string(calls), "run --rm")
	assert.NotContains(t, string(calls), "devsy-agent-init-")
	assert.Equal(t, 1, sourceCalls)
}

func TestPopulateVolumeDirectCopy_PodmanWritesDirectlyWhenWritable(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	destPath := filepath.Join(mountDir, binaryName())
	binaryContent := []byte("fake-agent-binary-content")

	scriptPath := filepath.Join(tmpDir, "podman")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  unshare) echo \"unexpected unshare\" >&2; exit 1 ;;\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath, Runtime: docker.RuntimePodman}
	err := d.populateVolumeDirectCopy(context.Background(), "test-vol", binaryContent)
	require.NoError(t, err)

	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPopulateVolumeDirectCopy_PodmanFallsBackToUnshareOnPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions; EACCES fallback cannot be exercised")
	}
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))
	require.NoError(
		t,
		os.Chmod(mountDir, 0o550),
	) // #nosec G302 -- test sets restrictive permissions to trigger EACCES
	t.Cleanup(func() { _ = os.Chmod(mountDir, 0o750) }) // #nosec G302 -- restore for cleanup

	destPath := filepath.Join(mountDir, binaryName())
	binaryContent := []byte("fake-agent-binary-content")
	scriptPath := filepath.Join(tmpDir, "podman")
	require.NoError(t, os.WriteFile(scriptPath, []byte(
		"#!/bin/sh\n"+
			"case \"$1\" in\n"+
			"  unshare) chmod 755 \""+mountDir+"\" 2>/dev/null; shift; exec \"$@\" ;;\n"+
			"  volume) echo \""+mountDir+"\" ;;\n"+
			"  *) exit 1 ;;\n"+
			"esac\n"), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath, Runtime: docker.RuntimePodman}
	err := d.populateVolumeDirectCopy(context.Background(), "test-vol", binaryContent)
	require.NoError(t, err)

	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPopulateVolumeDirectCopy_DockerUsesDirectWrite(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	binaryContent := []byte("fake-agent-binary-content")

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.populateVolumeDirectCopy(context.Background(), "test-vol", binaryContent)
	require.NoError(t, err)

	destPath := filepath.Join(mountDir, binaryName())
	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPopulateVolumeViaUnshare_FailureReturnsError(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "podman")
	script := "#!/bin/sh\necho 'unshare failed: permission denied' >&2; exit 1\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.populateVolumeViaUnshare(context.Background(), "/fake/path/devsy", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "podman unshare write failed")
}

func TestDetectVolumeVersion_ReturnsVersionFromHelper(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  run) echo \"v1.2.3\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	ver := d.detectVolumeVersion(context.Background(), "test-vol")
	assert.Equal(t, "v1.2.3", ver)
}

func TestDetectVolumeVersion_ReturnsEmptyOnFailure(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\nexit 1\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	ver := d.detectVolumeVersion(context.Background(), "test-vol")
	assert.Empty(t, ver)
}

func TestDetectVolumeVersion_IgnoresStderrNoise(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  run)\n" +
		"    echo \"Unable to find image 'busybox:latest' locally\" >&2\n" +
		"    echo \"latest: Pulling from library/busybox\" >&2\n" +
		"    echo \"v1.2.3\"\n" +
		"    ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	ver := d.detectVolumeVersion(context.Background(), "test-vol")
	assert.Equal(t, "v1.2.3", ver)
}

func TestDetectVolumeVersion_ReturnsEmptyWhenNoBinary(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  run) echo \"\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	ver := d.detectVolumeVersion(context.Background(), "test-vol")
	assert.Empty(t, ver)
}

func TestDeliverPreStart_VersionMatch_SkipsPopulate(t *testing.T) {
	tmpDir := t.TempDir()

	populateCalled := false
	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	markerPath := filepath.Join(tmpDir, "populate-called")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume) echo \"ok\" ;;\n" +
		"  run)\n" +
		"    if echo \"$@\" | grep -q \"\\-i\"; then\n" +
		"      touch \"" + markerPath + "\"\n" +
		"      cat > /dev/null\n" +
		"    else\n" +
		"      echo \"v2.0.0\"\n" +
		"    fi\n" +
		"    ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	binarySource := func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte("binary"))), nil
	}

	d := &LocalDockerDelivery{
		DockerCommand:   scriptPath,
		ExpectedVersion: "v2.0.0",
	}

	opts := PreStartOptions{
		WorkspaceID:  "test-ws",
		RunOptions:   &driver.RunOptions{},
		BinarySource: binarySource,
		Arch:         testArch,
	}

	err := d.DeliverPreStart(context.Background(), opts)
	require.NoError(t, err)

	_, statErr := os.Stat(markerPath)
	populateCalled = statErr == nil
	assert.False(t, populateCalled, "populateVolume should not be called when versions match")
}

func TestDeliverPreStart_VersionMismatch_Overwrites(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume)\n" +
		"    case \"$2\" in\n" +
		"      create) echo \"ok\" ;;\n" +
		"      inspect) echo \"" + mountDir + "\" ;;\n" +
		"      *) exit 1 ;;\n" +
		"    esac\n" +
		"    ;;\n" +
		"  run)\n" +
		"    if echo \"$@\" | grep -q \"\\-i\"; then\n" +
		"      echo \"image not found\" >&2; exit 1\n" +
		"    else\n" +
		"      echo \"v1.0.0\"\n" +
		"    fi\n" +
		"    ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	binaryContent := []byte("new-agent-binary")
	binarySource := func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(binaryContent)), nil
	}

	d := &LocalDockerDelivery{
		DockerCommand:   scriptPath,
		ExpectedVersion: "v2.0.0",
	}

	opts := PreStartOptions{
		WorkspaceID:  "test-ws",
		RunOptions:   &driver.RunOptions{},
		BinarySource: binarySource,
		Arch:         testArch,
	}

	err := d.DeliverPreStart(context.Background(), opts)
	require.NoError(t, err)

	destPath := filepath.Join(mountDir, binaryName())
	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)
}

func TestExpectedVersion_UsesFieldWhenSet(t *testing.T) {
	d := &LocalDockerDelivery{ExpectedVersion: "v3.0.0"}
	assert.Equal(t, "v3.0.0", d.expectedVersion())
}

func TestExpectedVersion_FallsBackToGetVersion(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.NotEmpty(t, d.expectedVersion())
}

func TestLocalDockerDelivery_Cleanup_RemovesManagedVolumes(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "calls.log")

	// Fake docker: `volume ls` lists two managed volumes; `volume rm` records
	// the removed name so the test can assert both were cleaned up.
	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"ls\" ]; then\n" +
		"  printf 'devsy-agent-ws1\\nws1-workspace\\n'; exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"rm\" ]; then\n" +
		"  echo \"$@\" >> \"" + logPath + "\"; exit 0\n" +
		"fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	require.NoError(t, d.Cleanup(context.Background(), "ws1"))

	logged, err := os.ReadFile(logPath) //nolint:gosec // test reads a temp file we control
	require.NoError(t, err)
	removed := string(logged)
	assert.Contains(t, removed, "devsy-agent-ws1")
	assert.Contains(t, removed, "ws1-workspace")
}

func TestLocalDockerDelivery_Cleanup_RemovesLegacyUnlabeledAgentVolume(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "calls.log")

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"ls\" ]; then exit 0; fi\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"rm\" ]; then\n" +
		"  echo \"$@\" >> \"" + logPath + "\"; exit 0\n" +
		"fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	require.NoError(t, d.Cleanup(context.Background(), "ws-legacy"))

	logged, err := os.ReadFile(logPath) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "volume rm -f devsy-agent-ws-legacy\n", string(logged))
}

func TestLocalDockerDelivery_SeedExcludesBuildInternal(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "run.log")

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"inspect\" ]; then exit 1; fi\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"create\" ]; then exit 0; fi\n" +
		"if [ \"$1\" = \"run\" ]; then echo \"$@\" >> \"" + logPath + "\"; exit 0; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.SeedWorkspaceVolume(context.Background(), WorkspaceSeedOptions{
		WorkspaceID: testSeedWorkspaceID,
		VolumeName:  testSeedVolumeName,
		SourceDir:   testSeedSourceDir,
	})
	require.NoError(t, err)

	logged, err := os.ReadFile(logPath) //nolint:gosec // test reads a temp file we control
	require.NoError(t, err)
	assert.Contains(t, string(logged), "--exclude")
	assert.Contains(t, string(logged), ".devsy-internal")
}

func TestLocalDockerDelivery_Seed_CopyAndCleanupFailureJoined(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"inspect\" ]; then exit 1; fi\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"create\" ]; then exit 0; fi\n" +
		"if [ \"$1\" = \"volume\" ] && [ \"$2\" = \"rm\" ]; then echo 'rm boom' 1>&2; exit 1; fi\n" +
		"if [ \"$1\" = \"run\" ]; then echo 'copy boom' 1>&2; exit 1; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.SeedWorkspaceVolume(context.Background(), WorkspaceSeedOptions{
		WorkspaceID: testSeedWorkspaceID,
		VolumeName:  testSeedVolumeName,
		SourceDir:   testSeedSourceDir,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seed workspace volume")
	assert.Contains(t, err.Error(), "remove partial volume")
}

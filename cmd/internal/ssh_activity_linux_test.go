//go:build linux

package cmdinternal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackedStdioUsesMountedManagedConfigWithoutEnvironmentOrDiscovery(t *testing.T) {
	if os.Getenv("DEVSY_MOUNTED_SECRET_TEST") == "" {
		t.Skip("requires the isolated root container with a private secret mount")
	}
	require.Zero(t, os.Geteuid())
	t.Setenv(config.EnvWorkspaceDaemonConfig, "")
	encoded, err := agentd.EncodeDaemonConfig(&agentd.DaemonConfig{
		Timeout: "1m", Lifecycle: agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon},
	})
	require.NoError(t, err)
	// #nosec G301 -- mirror a Kubernetes secret directory traversable by the nonroot child.
	require.NoError(t, os.MkdirAll(filepath.Dir(agentd.ContainerDaemonConfigPath), 0o755))
	// #nosec G306 -- mirror the readable, root-owned Kubernetes projected secret.
	require.NoError(t, os.WriteFile(agentd.ContainerDaemonConfigPath, []byte(encoded), 0o644))
	binary, err := os.Executable()
	require.NoError(t, err)
	// #nosec G204 -- re-execute the current test binary with fixed test selection.
	cmd := exec.Command(binary, "-test.run=^TestMountedManagedConfigStdioChild$")
	cmd.Env = append(os.Environ(), "DEVSY_MOUNTED_SECRET_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}},
		Pdeathsig:  syscall.SIGKILL,
	}
	output, err := cmd.CombinedOutput()
	t.Logf("nonroot stdio caller: %s", output)
	require.NoError(t, err)
}

func TestMountedManagedConfigStdioChild(t *testing.T) {
	if os.Getenv("DEVSY_MOUNTED_SECRET_CHILD") == "" {
		return
	}
	require.Equal(t, 65534, os.Geteuid())
	require.Empty(t, os.Getenv(config.EnvWorkspaceDaemonConfig))
	_, err := os.Lstat(config.ContainerLifecycleDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := &sshServerCmd{stdio: true, trackActivity: true, address: "127.0.0.1:0", workdir: "/tmp"}
	err = cmd.run(ctx)
	assert.ErrorIs(
		t,
		err,
		agentd.ErrLifecycleOwnerMissing,
		"mounted managed configuration must refuse untracked stdio before SSH work, even without discovery",
	)
}

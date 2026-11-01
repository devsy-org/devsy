package apple

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
)

const tmpfsIntegrationImage = "ghcr.io/devsy-org/test-images/base:ubuntu"

// TestTmpfsMountsIntegration starts a container with the tmpfs mounts Devsy
// generates for secrets and checks the native CLI accepts and honors them.
// It is gated behind DEVSY_APPLE_E2E=1 (Apple silicon, macOS 26+, `container`
// installed) and never runs in normal CI.
func TestTmpfsMountsIntegration(t *testing.T) {
	if os.Getenv("DEVSY_APPLE_E2E") != "1" {
		t.Skip("set DEVSY_APPLE_E2E=1 to run against a real container CLI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	helper := &apple.AppleHelper{Command: "container"}
	require.NoError(t, helper.EnsureSystemRunning(ctx))

	var out bytes.Buffer
	err := helper.Pull(ctx, apple.PullOptions{
		Image:  tmpfsIntegrationImage,
		Stdout: &out,
		Stderr: &out,
	})
	require.NoError(t, err, out.String())

	name := fmt.Sprintf("devsy-tmpfs-%d", time.Now().UnixNano())
	label := "devsy.e2e.tmpfs=" + name
	// Registered before run so a partially created container is still removed.
	t.Cleanup(func() { cleanupTmpfsProbe(t, helper, name, label) })

	args := (&appleDriver{}).buildRunArgs(tmpfsIntegrationParams(name, label))
	out.Reset()
	err = helper.Run(ctx, args, apple.Streams{Stdout: &out, Stderr: &out})
	require.NoError(t, err, out.String())
	require.NoError(t, helper.WaitContainerRunning(ctx, name))

	out.Reset()
	err = helper.Run(
		ctx,
		[]string{"exec", name, "sh", "-c", tmpfsIntegrationOracle},
		apple.Streams{Stdout: &out, Stderr: &out},
	)
	require.NoError(t, err, out.String())
	t.Log(out.String())
}

// cleanupTmpfsProbe removes the probe container if it exists. Failures are
// reported with t.Errorf so they never replace the first failing assertion,
// and a container that was never created is not an error.
func cleanupTmpfsProbe(t *testing.T, helper *apple.AppleHelper, name, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	found, err := helper.FindDevContainer(ctx, []string{label})
	if err != nil {
		t.Errorf("list probe container %s: %v", name, err)
		return
	}
	if found == nil {
		return
	}
	if err := helper.Stop(ctx, name); err != nil {
		t.Logf("stop probe container %s: %v", name, err)
	}
	if err := helper.Remove(ctx, name); err != nil {
		t.Errorf("remove probe container %s: %v", name, err)
	}
}

func tmpfsIntegrationParams(name, label string) *driver.RunImageDevContainerParams {
	return &driver.RunImageDevContainerParams{
		WorkspaceID: name,
		ParsedConfig: &config.DevContainerConfig{
			NonComposeBase: config.NonComposeBase{
				RunArgs: []string{"--name", name, "--cpus", "1", "--memory", "256M"},
			},
		},
		Options: &driver.RunOptions{
			Image:      tmpfsIntegrationImage,
			Entrypoint: "/bin/sh",
			Cmd:        []string{"-c", "sleep 120"},
			Labels:     []string{label},
			Mounts: []*config.Mount{
				// Docker-style options exactly as the shared secret mounts emit them.
				{
					Type:   driver.MountTypeTmpfs,
					Target: config.SecretsMountDir,
					Other:  []string{"tmpfs-mode=0755"},
				},
				{
					Type:   driver.MountTypeTmpfs,
					Target: config.SecretsEnvDir,
					Other:  []string{"tmpfs-mode=0700"},
				},
				// Native options pass through unchanged. Whole-MiB sizes only:
				// container 1.5.0 turns smaller sizes into zero (uncapped).
				{
					Type:   driver.MountTypeTmpfs,
					Target: "/probe-native",
					Other:  []string{"mode=0700", "size=1M"},
				},
				{
					Type:   driver.MountTypeTmpfs,
					Target: "/probe-size",
					Other:  []string{"size=2M"},
				},
				{Type: driver.MountTypeTmpfs, Target: "/probe-ro", Other: []string{"readonly"}},
			},
		},
	}
}

// Capacity is block size times block count, so the check does not depend on
// the guest page geometry.
const tmpfsIntegrationOracle = `set -eu
bytes() { echo $(( $(stat -f -c %S "$1") * $(stat -f -c %b "$1") )); }
stat -c '%n %a' /run/secrets /run/devsy/secrets-env /probe-native
test "$(stat -c %a /run/secrets)" = 755
test "$(stat -c %a /run/devsy/secrets-env)" = 700
test "$(stat -c %a /probe-native)" = 700
for p in /run/secrets /run/devsy/secrets-env /probe-native /probe-size /probe-ro; do
 test "$(stat -f -c %T "$p")" = tmpfs
done
test "$(bytes /probe-native)" = 1048576
test "$(bytes /probe-size)" = 2097152
touch /probe-native/writable
if touch /probe-ro/forbidden 2>/dev/null; then exit 1; fi
echo 'native CLI tmpfs mount behavior verified'
`

package wslg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// CI supplies a real WSL distro/user. Ordinary Windows unit runs need no WSL.
func TestWSLgNativeProbe(t *testing.T) {
	distro := os.Getenv("DEVSY_WSLG_TEST_DISTRO")
	if distro == "" {
		t.Skip("requires the Windows WSL CI fixture")
	}
	target := Target{Distro: distro, User: os.Getenv("DEVSY_WSLG_TEST_USER")}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path, err := systemWSLPath()
	require.NoError(t, err)
	dir, err := windows.GetSystemDirectory()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "wsl.exe"), path)
	// Verify the GNU printenv missing-key convention against the real distro.
	result, err := (commandRunner{}).Run(
		ctx,
		path,
		[]string{
			"--distribution",
			target.Distro,
			"--user",
			target.User,
			"--exec",
			"/usr/bin/printenv",
			"DEVSY_WSLG_TEST_UNSET_1464",
		},
	)
	require.NoError(t, err)
	require.Equal(t, 1, result.ExitCode)
	require.Empty(t, result.Stdout)
	require.Empty(t, result.Stderr)
	snapshot, err := Discover(ctx, target)
	require.NoError(
		t,
		err,
		"fixed noninteractive WSLg discovery must work; this runner cannot prove visible Windows windows",
	)
	require.NotEmpty(t, snapshot.Environment())
	for key := range snapshot.Environment() {
		t.Logf("discovered %s", key)
	}
}

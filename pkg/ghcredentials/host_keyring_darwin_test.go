//go:build darwin

package ghcredentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeMacKeyringStoredActiveAccount(t *testing.T) {
	if os.Getenv("DEVSY_TEST_NATIVE_KEYRING") != "1" {
		t.Skip("native keyring integration is opt-in and required by the macOS CI job")
	}
	requireRealGH(t)
	random := make([]byte, 12)
	_, err := rand.Read(random)
	require.NoError(t, err)
	host := "devsy-test-" + hex.EncodeToString(random) + ".invalid"
	token := "dummy-native-keyring-token"
	addDummyKeyring(t, host, token)
	dir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, hostsFile),
			[]byte(host+":\n    user: fixture-user\n    users:\n        fixture-user:\n"),
			0o600,
		),
	)
	t.Setenv("GH_CONFIG_DIR", dir)
	result := HostAccounts(context.Background())
	require.Equal(t, stateAvailable, result.Status)
	require.Len(t, result.Accounts, 1)
	require.True(
		t,
		result.Accounts[0] == (Account{Host: host, User: "fixture-user", Token: token}),
		"stored active account must resolve through the native keyring",
	)
	data, err := readFile(filepath.Join(dir, hostsFile))
	require.NoError(t, err)
	require.False(
		t,
		strings.Contains(string(data), token),
		"host discovery must not publish keyring credentials to YAML",
	)
}

func addDummyKeyring(t *testing.T, host, token string) {
	t.Helper()
	service := "gh:" + host
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	add := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	add.Stdin = strings.NewReader(
		fmt.Sprintf("add-generic-password -s %s -a fixture-user -w %s\n", service, token),
	)
	add.WaitDelay = 250 * time.Millisecond
	created := false
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		//nolint:gosec // The service is the random test-owned keyring item, never an existing account.
		remove := exec.CommandContext(
			cleanup,
			"/usr/bin/security",
			"delete-generic-password",
			"-s",
			service,
			"-a",
			"fixture-user",
		)
		remove.WaitDelay = 250 * time.Millisecond
		removeErr := remove.Run()
		if created {
			require.NoError(t, removeErr, "could not remove the test-owned keyring item")
		}
	})
	addErr := add.Run()
	created = addErr == nil
	require.NoError(t, addErr, "could not create the uniquely named dummy keyring item")
}

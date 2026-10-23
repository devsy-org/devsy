package ghcredentials

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requireRealGH(t *testing.T) {
	t.Helper()
	if testing.Short() {
		// The CPU-contended -short unit tier can exceed the 1.5s token lookup
		// budget; the GitHub Credential Host Tests job runs these uncontended.
		t.Skip("real gh runs in the GitHub Credential Host Tests job")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		if os.Getenv("DEVSY_TEST_REAL_GH") == "1" {
			require.NoError(t, err)
		}
		t.Skip("real gh is not installed; CI requires this native test")
	}
}

func TestNativeRealCLIStoredAccountsIgnoreHostTokenEnvironment(t *testing.T) {
	requireRealGH(t)
	dir := filepath.Join(t.TempDir(), "config with spaces")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hostsFile), []byte(`github.com:
    user: stored-user
    oauth_token: dummy-stored-token
    users:
        stored-user:
            oauth_token: dummy-stored-token
enterprise.example.invalid:
    user: enterprise-user
    oauth_token: dummy-enterprise-token
`), 0o600))
	t.Setenv("GH_CONFIG_DIR", dir)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "dummy-unrelated-environment-token")
	}
	result := HostAccounts(context.Background())
	require.Equal(t, stateAvailable, result.Status)
	require.Len(t, result.Accounts, 2)
	// Boolean assertions keep even the generated tokens out of failure output.
	require.True(
		t,
		result.Accounts[0] == (Account{Host: "github.com", User: "stored-user", Token: "dummy-stored-token"}),
	)
	require.True(
		t,
		result.Accounts[1] == (Account{
			Host: "enterprise.example.invalid", User: "enterprise-user", Token: "dummy-enterprise-token",
		}),
	)
	require.Equal(t, "dummy-unrelated-environment-token", os.Getenv("GH_TOKEN"))
}

func TestNativeRealCLIScalarUsernamesAcceptedByRelay(t *testing.T) {
	requireRealGH(t)
	dir := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", dir)
	binary, err := exec.LookPath("gh")
	require.NoError(t, err)
	const host = "scalar-user-fixture.invalid"
	for _, user := range []string{"12345", testScalarBooleanUser} {
		data := host + ":\n  user: " + user + "\n  users:\n    " + user +
			":\n      oauth_token: dummy-scalar-user-token\n"
		_, err := decodeHosts([]byte(data))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, hostsFile), []byte(data), 0o600))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		//nolint:gosec // Installed CLI, private dummy configuration, and a reserved invalid hostname.
		cmd := exec.CommandContext(ctx, binary, "auth", "token", "--hostname", host, "--user", user)
		cmd.Env = hostLookupEnv(os.Environ())
		cmd.WaitDelay = 250 * time.Millisecond
		out := &tokenOutput{}
		err = runTokenProbe(cmd, out)
		cancel()
		require.NoError(t, err)
		require.True(t, strings.TrimSpace(string(out.data)) == "dummy-scalar-user-token")
	}
}

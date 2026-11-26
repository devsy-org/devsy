package ghcredentials

import (
	"context"
	"errors"
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
		// budget; the native credential host CI steps run these uncontended.
		t.Skip("real gh runs in the native credential host CI steps")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		if os.Getenv("DEVSY_TEST_REAL_GH") == "1" {
			require.NoError(t, err)
		}
		t.Skip("real gh is not installed; CI requires this native test")
	}
}

const realGHTokenLookupFailed = "token_lookup_failed"

var errRealGHStalled = errors.New("real gh lookup stalled")

// retryRealGHStall reruns check once when it reports a stalled gh process.
// A single real gh call has stalled for seconds on shared Windows runners;
// every other failure is final, and so is a second stall.
func retryRealGHStall(logf func(string, ...any), check func() (stalled bool, err error)) error {
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		start := time.Now()
		var stalled bool
		stalled, err = check()
		logf(
			"real gh attempt %d took %s, stalled=%t",
			attempt,
			time.Since(start).Round(time.Millisecond),
			stalled,
		)
		if err == nil || !stalled {
			return err
		}
	}
	return err
}

func realGHScalarToken(binary, host, user string) (output string, stalled bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//nolint:gosec // Installed CLI, private dummy configuration, and a reserved invalid hostname.
	cmd := exec.CommandContext(ctx, binary, "auth", "token", "--hostname", host, "--user", user)
	cmd.Env = hostLookupEnv(os.Environ())
	cmd.WaitDelay = 250 * time.Millisecond
	out := &tokenOutput{}
	err = runTokenProbe(cmd, out)
	if ctx.Err() != nil {
		return "", true, errors.Join(errRealGHStalled, err)
	}
	return strings.TrimSpace(string(out.data)), false, err
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
	var result HostResult
	require.NoError(t, retryRealGHStall(t.Logf, func() (bool, error) {
		result = HostAccounts(context.Background())
		if result.Status == realGHTokenLookupFailed {
			return true, errRealGHStalled
		}
		return false, nil
	}))
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
		var got string
		require.NoError(t, retryRealGHStall(t.Logf, func() (bool, error) {
			var stalled bool
			var err error
			got, stalled, err = realGHScalarToken(binary, host, user)
			return stalled, err
		}))
		require.True(t, got == "dummy-scalar-user-token")
	}
}

func TestRetryRealGHStall(t *testing.T) {
	type outcome struct {
		stalled bool
		err     error
	}
	errOther := errors.New("unrelated failure")
	stall := outcome{true, errRealGHStalled}
	ok := outcome{}
	tests := []struct {
		name      string
		outcomes  []outcome
		wantCalls int
		wantErr   error
	}{
		{"first attempt succeeds", []outcome{ok}, 1, nil},
		{"one stall then success", []outcome{stall, ok}, 2, nil},
		{"two stalls fail", []outcome{stall, stall}, 2, errRealGHStalled},
		{"other error is not retried", []outcome{{false, errOther}, ok}, 1, errOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, logged := 0, 0
			err := retryRealGHStall(
				func(string, ...any) { logged++ },
				func() (bool, error) {
					calls++
					return tt.outcomes[calls-1].stalled, tt.outcomes[calls-1].err
				},
			)
			require.Equal(t, tt.wantCalls, calls)
			require.Equal(t, tt.wantCalls, logged)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

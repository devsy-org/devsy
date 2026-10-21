package ghcredentials

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testPathEnv        = "PATH=/usr/bin:/bin"
	testForwardedToken = "dummy-forwarded-value"
	testHost           = "github.example"
	testUser           = "relay-user"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestMain(m *testing.M) {
	if mode := os.Getenv("DEVSY_TEST_GH_HELPER"); mode != "" {
		os.Exit(runTokenHelper(mode))
	}
	os.Exit(m.Run())
}

const (
	helperPipes      = "pipes"
	helperPipesError = "pipes-error"
	helperStall      = "stall"
	helperLarge      = "large"
	helperExitOne    = "exit1"
	helperCancel     = "cancel"
	allStalled       = "all-stalled"
	fastHost         = "fast.one"
)

const (
	secondFastHost      = "fast.two"
	cancelledCollection = "cancelled"
)

const firstSlowHost = "slow.one"

func runTokenHelper(mode string) int {
	switch mode {
	case "hold", helperStall:
		time.Sleep(10 * time.Second)
		return 0
	case helperPipes, helperPipesError:
		return runPipeHelper(mode)
	case helperLarge:
		_, _ = os.Stdout.WriteString(strings.Repeat("x", maxTokenOutput+1))
		return 0
	case helperExitOne:
		return 1
	case "collection":
		return runCollectionHelper()
	case "env":
		return runEnvironmentHelper()
	default:
		return 47
	}
}

func runPipeHelper(mode string) int {
	//nolint:gosec // Re-execute the owned native test binary as a pipe-holding child.
	cmd := exec.Command(os.Args[0])
	cmd.Env = replaceEnv(os.Environ(), "DEVSY_TEST_GH_HELPER", "hold")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if cmd.Start() != nil {
		return 42
	}
	if path := os.Getenv("DEVSY_TEST_CHILD_PID"); path != "" {
		_ = writeHelperFile(path, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
	_, _ = os.Stdout.WriteString("partial-dummy")
	if mode == helperPipesError {
		return 1
	}
	return 0
}

func runCollectionHelper() int {
	host := os.Args[len(os.Args)-1]
	if strings.HasPrefix(host, "slow") {
		time.Sleep(10 * time.Second)
		return 0
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if authEnvironment[strings.ToUpper(name)] {
			return 43
		}
	}
	path := filepath.Join(os.Getenv("GH_CONFIG_DIR"), hostsFile)
	b, err := readHelperFile(path)
	if err != nil || !strings.Contains(string(b), "baseline") {
		return 44
	}
	if writeHelperFile(path, []byte("migrated\n"), 0o600) != nil {
		return 45
	}
	_, _ = os.Stdout.WriteString("dummy-" + host)
	return 0
}

func runEnvironmentHelper() int {
	if os.Getenv(authProbeEnv) != "1" {
		return 46
	}
	_, _ = os.Stdout.WriteString(os.Getenv("GH_TOKEN"))
	return 0
}

func helperBinary(t *testing.T) string {
	t.Helper()
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	source, err := os.Executable()
	require.NoError(t, err)
	return source
}

func TestNativeTokenLookupBoundsAndRetainedPipes(t *testing.T) {
	binary := helperBinary(t)
	for _, mode := range []string{helperPipes, helperPipesError, helperStall, helperLarge, helperExitOne, helperCancel} {
		t.Run(mode, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "pid")
			t.Cleanup(func() { cleanupPipeHelper(t, pidPath) })
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			helperMode := mode
			if mode == helperCancel {
				helperMode = helperStall
				cancel()
			}
			env := replaceEnv(os.Environ(), "DEVSY_TEST_GH_HELPER", helperMode)
			env = replaceEnv(env, "DEVSY_TEST_CHILD_PID", pidPath)
			started := time.Now()
			token, err := resolveToken(ctx, binary, testHost, env)
			require.Error(t, err)
			require.True(t, token == "", "failed lookup returned partial token")
			require.Less(t, time.Since(started), 2*time.Second)
			if mode == helperPipes || mode == helperPipesError {
				require.ErrorIs(t, err, exec.ErrWaitDelay)
			}
			if mode == helperCancel {
				require.ErrorIs(t, err, context.Canceled)
			}
			if mode == helperLarge {
				require.ErrorIs(t, err, errTokenOutputLimit)
			}
			if mode != helperExitOne {
				require.False(t, noTokenResult(err))
			}
		})
	}
}

func TestNativeHostCollectionIsolatesIdentityAndMigration(t *testing.T) {
	for _, order := range []string{"fast-first", "slow-first", allStalled, cancelledCollection} {
		t.Run(order, func(t *testing.T) {
			binary := helperBinary(t)
			baseline := t.TempDir()
			writeTestFile(t, filepath.Join(baseline, hostsFile), "baseline: {}\n")
			hosts := mapping()
			list := collectionHosts(order)
			for _, host := range list {
				put(ensure(hosts, host), "user", scalar("stored-alice"))
			}
			env := replaceEnv(os.Environ(), "GH_CONFIG_DIR", baseline)
			env = replaceEnv(env, "DEVSY_TEST_GH_HELPER", "collection")
			for key := range authEnvironment {
				env = replaceEnv(env, key, "dummy-bob-env")
			}
			parent := strings.Join(env, "\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if order == cancelledCollection {
				cancel()
			}
			started := time.Now()
			result := collectHostAccounts(ctx, binary, hosts, env)
			require.Less(t, time.Since(started), 4*time.Second)
			require.Equal(t, "token_lookup_failed", result.Status)
			if order == allStalled || order == cancelledCollection {
				require.Empty(t, result.Accounts)
			} else {
				require.Len(t, result.Accounts, 2)
				require.Equal(
					t,
					[]string{fastHost, secondFastHost},
					[]string{result.Accounts[0].Host, result.Accounts[1].Host},
				)
				for _, a := range result.Accounts {
					require.Equal(t, "stored-alice", a.User)
					require.True(t, a.Token == "dummy-"+a.Host, "wrong host identity")
				}
			}
			require.True(t, strings.Join(env, "\n") == parent, "parent environment changed")
			b, err := readHelperFile(filepath.Join(baseline, hostsFile))
			require.NoError(t, err)
			require.True(t, string(b) == "baseline: {}\n", "baseline migrated")
		})
	}
}

func TestHostLookupEnvironmentAndGuestEnvironmentAreIndependent(t *testing.T) {
	env := []string{
		"GH_TOKEN=dummy-bob",
		"github_token=dummy-bob",
		"Gh_Enterprise_Token=dummy-bob",
		"GITHUB_ENTERPRISE_TOKEN=dummy-bob",
		"OTHER=keep",
		"GORACE=atexit_sleep_ms=0",
	}
	require.Equal(t, []string{"OTHER=keep", "GORACE=atexit_sleep_ms=0"}, hostLookupEnv(env))
	binary := helperBinary(t)
	env = replaceEnv(env, "DEVSY_TEST_GH_HELPER", "env")
	token, err := resolveToken(context.Background(), binary, "github.com", env)
	require.NoError(t, err)
	require.True(t, token == "dummy-bob", "guest environment was stripped")
	require.True(t, env[0] == "GH_TOKEN=dummy-bob", "parent environment was changed")
}

func TestRegisteredGuestDeclinesAmbiguousKeyringResult(t *testing.T) {
	binary := helperBinary(t)
	env := replaceEnv(os.Environ(), "DEVSY_TEST_GH_HELPER", helperExitOne)
	a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
	for _, body := range []string{"user: guest", "users: {guest: {}}", "oauth_token: ''\nuser: guest", "{}"} {
		n, err := decode([]byte(body))
		require.NoError(t, err)
		hosts := mapping()
		put(hosts, testHost, n)
		got := a.inject(context.Background(), binary, env, hosts)
		if body == "{}" {
			require.Equal(t, "forwarded", got)
		} else {
			require.Equal(t, "local_identity_unavailable", got)
			require.False(t, hasToken(get(hosts, testHost)))
		}
	}
}

func readHelperFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}

func writeHelperFile(path string, data []byte, mode os.FileMode) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(filepath.Base(path), data, mode)
}

func cleanupPipeHelper(t *testing.T, pidPath string) {
	t.Helper()
	if b, readErr := readHelperFile(pidPath); readErr == nil {
		pid, parseErr := strconv.Atoi(string(b))
		require.NoError(t, parseErr)
		child, findErr := os.FindProcess(pid)
		require.NoError(t, findErr)
		_ = child.Kill()
		_ = child.Release()
	}
}

func collectionHosts(order string) []string {
	list := []string{fastHost, firstSlowHost, secondFastHost, secondSlowHost}
	if order == "slow-first" {
		list = []string{firstSlowHost, secondSlowHost, fastHost, secondFastHost}
	}
	if order == allStalled {
		list = []string{firstSlowHost, secondSlowHost}
	}
	return list
}

const secondSlowHost = "slow.two"

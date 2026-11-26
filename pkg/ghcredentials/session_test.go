//go:build !windows

package ghcredentials

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionMergePreservesDotfilesAndConcurrentChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HOME", root)
	original := filepath.Join(root, "gh")
	target := filepath.Join(root, "dotfiles", configFile)
	writeTestFile(t, target, "editor: vi\npager: less\n")
	require.NoError(t, os.MkdirAll(original, 0o700))
	require.NoError(t, os.Symlink(target, filepath.Join(original, configFile)))
	s, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\npager: less\n")
	writeTestFile(t, target, "editor: emacs\npager: cat\nprompt: disabled\n")
	require.NoError(t, s.finish(context.Background()))
	link, err := os.Readlink(filepath.Join(original, configFile))
	require.NoError(t, err)
	require.Equal(t, target, link)
	got, err := readFile(target)
	require.NoError(t, err)
	n, err := decode(got)
	require.NoError(t, err)
	require.Equal(t, "nano", get(n, "editor").Value)
	require.Equal(t, "cat", get(n, "pager").Value)
	require.Equal(t, "disabled", get(n, "prompt").Value)
}

func TestSessionSnapshotIgnoresDanglingSymlinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	original := filepath.Join(root, "gh")
	writeTestFile(t, filepath.Join(original, configFile), "editor: vi\n")
	for _, name := range []string{"unused.yml", filepath.Join("extensions", "unused")} {
		path := filepath.Join(original, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.Symlink("missing-target", path))
	}
	s, err := startSession(context.Background(), original, []Account{
		{Host: testHost, User: testUser, Token: testForwardedToken},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.lock.Close() })
	env := []string{"HOME=" + root, "GH_CONFIG_DIR=" + original, testPathEnv}
	require.NoError(t, s.inject(context.Background(), "/usr/bin/false", env))
	data, err := readFile(filepath.Join(s.work(), hostsFile))
	require.NoError(t, err)
	require.True(t, bytes.Contains(data, []byte(testForwardedToken)), "forwarding was not prepared")
	writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
	require.NoError(t, s.finish(context.Background()))
	data, err = readFile(filepath.Join(original, configFile))
	require.NoError(t, err)
	require.Equal(t, "editor: nano\n", string(data))
	for _, name := range []string{"unused.yml", filepath.Join("extensions", "unused")} {
		target, err := os.Readlink(filepath.Join(original, name))
		require.NoError(t, err)
		require.Equal(t, "missing-target", target)
	}
	data, err = readFile(filepath.Join(original, hostsFile))
	require.NoError(t, err)
	require.False(
		t,
		bytes.Contains(data, []byte(testForwardedToken)),
		"forwarded credentials were published",
	)
	assertNoForwardedArtifacts(t, filepath.Join(root, "cache"))
}

func TestSessionSnapshotStillRejectsSymlinkCycles(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "cycle")
	require.NoError(t, os.Symlink("cycle", link))
	require.Error(t, copyTree(context.Background(), root, t.TempDir(), 0))
}

func TestSessionConcurrentInvocationsAndRecovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HOME", root)
	original := filepath.Join(root, "gh")
	writeTestFile(t, filepath.Join(original, configFile), "editor: vi\npager: less\n")
	a, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	b, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(a.work(), configFile), "editor: nano\npager: less\n")
	writeTestFile(t, filepath.Join(b.work(), configFile), "editor: vi\npager: cat\n")
	require.NoError(t, a.finish(context.Background()))
	require.NoError(t, b.lock.Close())
	c, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(c.work(), configFile))
	require.NoError(t, err)
	n, err := decode(got)
	require.NoError(t, err)
	require.Equal(t, "nano", get(n, "editor").Value)
	require.Equal(t, "cat", get(n, "pager").Value)
	require.NoError(t, c.finish(context.Background()))
}

func TestSessionDirectoryAliasesShareWritebackLock(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HOME", root)
	original, alias := filepath.Join(root, "gh"), filepath.Join(root, "alias")
	writeTestFile(t, filepath.Join(original, configFile), "editor: vi\npager: less\n")
	require.NoError(t, os.Symlink(original, alias))
	a, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.lock.Close() })
	b, err := startSession(context.Background(), alias, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.lock.Close() })
	writeTestFile(t, filepath.Join(a.work(), configFile), "editor: nano\npager: less\n")
	writeTestFile(t, filepath.Join(b.work(), configFile), "editor: vi\npager: cat\n")
	planned, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a.checkpoint = func(stage string) error {
		if stage == "planned:"+configFile {
			close(planned)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	// The short-lived context makes B prove it contends before A publishes.
	aDone := make(chan error, 1)
	go func() { aDone <- a.sync(ctx) }()
	select {
	case <-planned:
	case <-ctx.Done():
		t.Fatal("first writeback did not reach its publication barrier")
	}
	bCtx, bCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer bCancel()
	err = b.sync(bCtx)
	close(release)
	require.ErrorIs(t, err, context.DeadlineExceeded, "alias bypassed the configuration lock")
	require.NoError(t, <-aDone)
	require.NoError(t, b.sync(context.Background()))
	assertMergedPreferences(t, original)
}

func assertMergedPreferences(t *testing.T, original string) {
	t.Helper()
	data, err := readFile(filepath.Join(original, configFile))
	require.NoError(t, err)
	n, err := decode(data)
	require.NoError(t, err)
	require.Equal(t, "nano", get(n, "editor").Value)
	require.Equal(t, "cat", get(n, "pager").Value)
}

func TestSessionRejectsRetargetedConfigurationDirectory(t *testing.T) {
	for _, stage := range []string{"before writeback", "planned:" + configFile} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
			one := filepath.Join(root, "one")
			two := filepath.Join(root, "two")
			alias := filepath.Join(root, "alias")
			writeTestFile(t, filepath.Join(one, configFile), "editor: vi\n")
			writeTestFile(t, filepath.Join(two, configFile), "editor: vi\n")
			require.NoError(t, os.Symlink(one, alias))
			s, err := startSession(context.Background(), alias, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.lock.Close() })
			writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
			retarget := func() {
				require.NoError(t, os.Remove(alias))
				require.NoError(t, os.Symlink(two, alias))
			}
			if stage == "before writeback" {
				retarget()
			} else {
				s.checkpoint = func(at string) error {
					if at == stage {
						retarget()
					}
					return nil
				}
			}
			require.Error(t, s.finish(context.Background()))
			assertNoForwardedArtifacts(t, s.dir)
			for _, dir := range []string{one, two} {
				data, err := readFile(filepath.Join(dir, configFile))
				require.NoError(t, err)
				require.Equal(t, "editor: vi\n", string(data))
			}
			require.NoError(t, os.Remove(alias))
			require.NoError(t, os.Symlink(one, alias))
			err = recoverSession(context.Background(), s.dir)
			if err != nil {
				require.ErrorIs(t, err, errAuthConflict)
			}
			assertNoForwardedArtifacts(t, filepath.Dir(s.dir))
		})
	}
}

func TestStripForwardedCredentialsAndPreserveLocalLogin(t *testing.T) {
	a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
	s := &session{Accounts: []Account{a}}
	for _, tc := range []struct {
		name, input    string
		local, keyring bool
	}{
		{"unchanged", `github.example:
  user: relay-user
  oauth_token: dummy-forwarded-value
  users:
    relay-user:
      oauth_token: dummy-forwarded-value
`, false, false},
		{"local login", `github.example:
  user: relay-user
  oauth_token: dummy-local-value
  users:
    relay-user:
      oauth_token: dummy-local-value
`, true, false},
		{"keyring login", "github.example:\n  user: relay-user\n  users:\n    relay-user: {}\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.strip(nil, []byte(tc.input))
			require.NoError(t, err)
			// Do not include the produced configuration in assertion failures.
			n, err := decode(got)
			require.NoError(t, err)
			h := get(n, a.Host)
			require.Equal(t, tc.local, h != nil)
			if tc.local {
				require.NotNil(t, get(h, "user"))
			}
			if tc.keyring {
				require.Nil(t, get(h, "oauth_token"))
			}
		})
	}
}

func TestMergeAddedMappingsAndDeletions(t *testing.T) {
	got, err := mergeYAML(
		[]byte("aliases: {}\neditor: vi\n"),
		[]byte("aliases:\n  mine: repo view\n"),
		[]byte("aliases:\n  other: issue list\neditor: vi\npager: cat\n"),
	)
	require.NoError(t, err)
	n, err := decode(got)
	require.NoError(t, err)
	require.Nil(t, get(n, "editor"))
	require.Equal(t, "cat", get(n, "pager").Value)
	require.NotNil(t, get(get(n, "aliases"), "mine"))
	require.NotNil(t, get(get(n, "aliases"), "other"))
}

func TestDanglingDotfilesSymlinkKeepsPendingChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	original, target := filepath.Join(root, "gh"), filepath.Join(root, "dotfiles.yml")
	writeTestFile(t, target, "editor: vi\n")
	require.NoError(t, os.MkdirAll(original, 0o700))
	link := filepath.Join(original, configFile)
	require.NoError(t, os.Symlink(target, link))
	s, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
	require.NoError(t, os.Remove(target))
	require.Error(t, s.finish(context.Background()))
	actual, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, target, actual)
	writeTestFile(t, target, "editor: emacs\npager: cat\n")
	c, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	data, err := readFile(filepath.Join(c.work(), configFile))
	require.NoError(t, err)
	n, err := decode(data)
	require.NoError(t, err)
	require.Equal(t, "nano", get(n, "editor").Value)
	require.Equal(t, "cat", get(n, "pager").Value)
	require.NoError(t, c.finish(context.Background()))
}

func TestSessionPreservesTokenlessRegisteredIdentity(t *testing.T) {
	const dotfilesUser = "dotfiles-user"
	for _, tc := range []struct {
		user, tokenField, userBody string
	}{
		{testUser, "", "{}"},
		{dotfilesUser, "", ""},
		{dotfilesUser, "", "\"\""},
		{"12345", "", "{}"},
		{testScalarBooleanUser, "", "{}"},
		{testUser, "  oauth_token: \"\"\n", "\n      oauth_token: \"\""},
		{dotfilesUser, "  oauth_token:\n", "\n      oauth_token:"},
	} {
		t.Run(tc.user+tc.tokenField, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
			original := filepath.Join(root, "gh")
			before := "github.example:\n  git_protocol: https\n  user: " + tc.user + "\n" + tc.tokenField +
				"  users:\n    " + tc.user + ": " + tc.userBody + "\n    other-user:\n"
			writeTestFile(t, filepath.Join(original, hostsFile), before)
			a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
			s, err := startSession(context.Background(), original, []Account{a})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.lock.Close() })
			env := []string{"HOME=" + root, "GH_CONFIG_DIR=" + original, testPathEnv}
			require.NoError(t, s.inject(context.Background(), "/usr/bin/false", env))
			data, err := readFile(filepath.Join(s.work(), hostsFile))
			require.NoError(t, err)
			n, err := decode(data)
			require.NoError(t, err)
			require.Empty(t, s.Accounts)
			originalNode, err := decode([]byte(before))
			require.NoError(t, err)
			require.True(t, equal(n, originalNode), "registered guest identity was replaced")
			require.NoError(t, s.finish(context.Background()))
			got, err := readFile(filepath.Join(original, hostsFile))
			require.NoError(t, err)
			// Never print credential-bearing configuration on assertion failure.
			require.True(t, string(got) == before, "tokenless dotfiles registration changed")
		})
	}
}

func TestAuthenticationProbeCannotMigrateOriginalSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	original := filepath.Join(root, "gh")
	before := "github.example:\n  user: dotfiles-user\n  users:\n    dotfiles-user: {}\n"
	writeTestFile(t, filepath.Join(original, hostsFile), before)
	probe := filepath.Join(root, "auth")
	writeTestFile(t, probe, `#!/bin/sh
printf '{}\n' > "$GH_CONFIG_DIR/hosts.yml"
printf 'version: "1"\n' > "$GH_CONFIG_DIR/config.yml"
exit 1
`)
	// resolveToken passes "auth" as the first argument, so sh reads this fixture.
	t.Chdir(root)
	s, err := startSession(context.Background(), original, []Account{
		{Host: testHost, User: testUser, Token: testForwardedToken},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.lock.Close() })
	env := []string{"HOME=" + root, "GH_CONFIG_DIR=" + original, testPathEnv}
	require.NoError(t, s.inject(context.Background(), "/bin/sh", env))
	for _, path := range []string{filepath.Join(original, hostsFile), filepath.Join(s.dir, "before", hostsFile)} {
		got, err := readFile(path)
		require.NoError(t, err)
		require.True(t, string(got) == before, "authentication probe modified original or baseline")
	}
	require.NoFileExists(t, filepath.Join(original, configFile))
	require.NoFileExists(t, filepath.Join(s.work(), configFile))
	require.NoError(t, s.finish(context.Background()))
	got, err := readFile(filepath.Join(original, hostsFile))
	require.NoError(t, err)
	require.True(t, string(got) == before, "probe migration was written back")
}

func TestForwardedLogoutRestoresHiddenPreferences(t *testing.T) {
	before := []byte(
		"github.example:\n  user: dotfiles-user\n  git_protocol: ssh\n  users:\n    dotfiles-user: {}\n",
	)
	s := &session{
		Accounts: []Account{
			{Host: testHost, User: testUser, Token: testForwardedToken},
		},
	}
	after, err := s.strip(before, []byte("{}\n"))
	require.NoError(t, err)
	original, err := decode(before)
	require.NoError(t, err)
	result, err := decode(after)
	require.NoError(t, err)
	require.True(t, equal(original, result), "forwarded logout removed hidden preferences")
}

func TestTokenLookupTimeoutDoesNotAllowForwarding(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
	hosts := mapping()
	decision := a.inject(ctx, "/usr/bin/false", []string{testPathEnv}, hosts)
	require.Equal(t, "local_lookup_failed", decision)
	require.Nil(t, get(hosts, a.Host))
}

func TestTokenLookupIsLocalOnlyAndChildScoped(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DEVSY_GH_AUTH_PROBE", "parent-value")
	writeTestFile(t, filepath.Join(root, "auth"), `#!/bin/sh
test "$DEVSY_GH_AUTH_PROBE" = 1 || exit 42
test "$DEVSY_GH_SESSION" = existing-session || exit 43
printf '%s' "$GH_TOKEN"
`)
	env := []string{
		testPathEnv,
		"DEVSY_GH_AUTH_PROBE=0",
		"DEVSY_GH_SESSION=existing-session",
		"GH_TOKEN=dummy-local-value",
	}
	token, err := resolveToken(context.Background(), "/bin/sh", testHost, env)
	require.NoError(t, err)
	require.True(t, "GH_TOKEN="+token == env[3], "local environment authentication changed")
	require.Equal(t, "DEVSY_GH_AUTH_PROBE=0", env[1])
	require.Equal(t, "parent-value", os.Getenv("DEVSY_GH_AUTH_PROBE"))
}

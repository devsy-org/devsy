//go:build !windows

package ghcredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func journalSession(t *testing.T) *session {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	original := filepath.Join(root, "original")
	writeTestFile(t, filepath.Join(original, configFile), "editor: vi\n")
	s, err := startSession(context.Background(), original, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.lock.Close() })
	a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
	s.Accounts = []Account{a}
	s.Overlays = []overlayAccount{fingerprint(a)}
	require.NoError(t, s.save())
	hosts := mapping()
	a.overlay(hosts)
	b, err := yaml.Marshal(hosts)
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(s.work(), hostsFile), string(b))
	return s
}

func assertNoForwardedArtifacts(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		data, err := readHelperFile(path)
		if err == nil {
			require.False(
				t,
				bytes.Contains(data, []byte(testForwardedToken)),
				"forwarded value retained in session artifact",
			)
		}
		return err
	})
	require.NoError(t, err)
}

func TestJournalSanitizesBeforeOriginalReadAndLegacyRecovery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy"}[legacy], func(t *testing.T) {
			s := journalSession(t)
			writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
			writeTestFile(t, filepath.Join(s.Original, configFile), "[invalid yaml")
			if legacy {
				b, err := json.Marshal(
					map[string]any{"original": s.Original, "accounts": s.Accounts},
				)
				require.NoError(t, err)
				writeTestFile(t, filepath.Join(s.dir, "session.json"), string(b))
				require.NoError(t, s.lock.Close())
				require.Error(t, recoverSession(context.Background(), s.dir))
			} else {
				require.Error(t, s.finish(context.Background()))
			}
			assertNoForwardedArtifacts(t, s.dir)
			writeTestFile(t, filepath.Join(s.Original, configFile), "editor: vi\n")
			require.NoError(t, recoverSession(context.Background(), s.dir))
			assertNoForwardedArtifacts(t, filepath.Dir(s.dir))
		})
	}
}

func TestJournalCheckpointRecoveryNeverReplaysLaterLogout(t *testing.T) {
	for _, stage := range []string{
		"sanitized", "planned:config.yml", "published:config.yml", "applied:config.yml",
		"planned:hosts.yml", "published:hosts.yml", "applied:hosts.yml",
	} {
		t.Run(stage, func(t *testing.T) {
			s := journalSession(t)
			writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
			writeTestFile(
				t,
				filepath.Join(s.work(), hostsFile),
				authFixture("local-user", "dummy-local-login"),
			)
			s.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("injected checkpoint interruption")
				}
				return nil
			}
			require.Error(t, s.finish(context.Background()))
			assertNoForwardedArtifacts(t, s.dir)
			// A new user operation after any partial publication must win recovery.
			writeTestFile(
				t,
				filepath.Join(s.Original, hostsFile),
				authFixture("new-user", "dummy-new-login"),
			)
			err := recoverSession(context.Background(), s.dir)
			if err != nil {
				require.ErrorIs(t, err, errAuthConflict)
			}
			b, err := readFile(filepath.Join(s.Original, hostsFile))
			require.NoError(t, err)
			n, err := decode(b)
			require.NoError(t, err)
			require.Equal(t, "new-user", get(get(n, testHost), "user").Value)
			require.True(
				t,
				get(get(n, testHost), "oauth_token").Value == "dummy-new-login",
				"recovery replayed old login",
			)
			require.NoError(t, recoverSession(context.Background(), s.dir))
		})
	}
}

func TestJournalMalformedWorkIsDiscardedWithoutRetainingForwardedToken(t *testing.T) {
	s := journalSession(t)
	writeTestFile(t, filepath.Join(s.work(), hostsFile), "[malformed "+testForwardedToken)
	require.Error(t, s.finish(context.Background()))
	assertNoForwardedArtifacts(t, s.dir)
	require.NoError(t, recoverSession(context.Background(), s.dir))
	require.NoFileExists(t, filepath.Join(s.Original, hostsFile))
}

func TestJournalPreservesEditsDuringPublishingCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "login", data: []byte(authFixture("new-user", "dummy-new-login"))},
		{name: "logout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := journalSession(t)
			baseline := authFixture("before-user", "dummy-before-login")
			writeTestFile(t, filepath.Join(s.dir, "before", hostsFile), baseline)
			writeTestFile(t, filepath.Join(s.Original, hostsFile), baseline)
			writeTestFile(
				t,
				filepath.Join(s.work(), hostsFile),
				authFixture("local-user", "dummy-local-login"),
			)
			path := filepath.Join(s.Original, hostsFile)
			s.checkpoint = func(stage string) error {
				if stage == "publishing:"+hostsFile {
					if tc.data == nil {
						return removeFile(path)
					}
					return os.WriteFile(path, tc.data, 0o600)
				}
				return nil
			}
			require.ErrorIs(t, s.finish(context.Background()), errAuthConflict)
			got, err := readFile(path)
			require.NoError(t, err)
			require.True(t, bytes.Equal(got, tc.data), "concurrent authentication edit overwritten")
			require.NoError(t, recoverSession(context.Background(), s.dir))
			got, err = readFile(path)
			require.NoError(t, err)
			require.True(t, bytes.Equal(got, tc.data), "recovery replayed earlier authentication")
			assertNoForwardedArtifacts(t, s.dir)
		})
	}
}

const duringPublishingStage = "during-publishing"

func TestJournalBindsPublicationToSymlinkTarget(t *testing.T) {
	for _, stage := range []string{"planned:", "publishing:", duringPublishingStage} {
		t.Run(stage, func(t *testing.T) {
			testJournalBindsPublicationToSymlinkTarget(t, stage)
		})
	}
}

func testJournalBindsPublicationToSymlinkTarget(t *testing.T, stagePrefix string) {
	s := journalSession(t)
	one, two := filepath.Join(s.Original, "one"), filepath.Join(s.Original, "two")
	writeTestFile(t, one, "editor: vi\n")
	writeTestFile(t, two, "editor: other\n")
	path := filepath.Join(s.Original, configFile)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(one, path))
	writeTestFile(t, filepath.Join(s.work(), configFile), "editor: nano\n")
	retarget := func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(two, path))
	}
	s.checkpoint = func(stage string) error {
		if stagePrefix == duringPublishingStage && stage == "publishing:"+configFile {
			retarget()
			return nil
		}
		if stage == stagePrefix+configFile {
			return errors.New("injected")
		}
		return nil
	}
	require.Error(t, s.finish(context.Background()))
	if stagePrefix != duringPublishingStage {
		retarget()
	}
	require.ErrorIs(t, recoverSession(context.Background(), s.dir), errAuthConflict)
	b, err := readFile(two)
	require.NoError(t, err)
	require.True(t, string(b) == "editor: other\n", "different symlink target overwritten")
}

func TestRecoveryLeavesLiveChildSessionIntact(t *testing.T) {
	s := journalSession(t)
	require.NoError(t, recoverSession(context.Background(), s.dir))
	b, err := readFile(filepath.Join(s.work(), hostsFile))
	require.NoError(t, err)
	require.True(t, bytes.Contains(b, []byte(testForwardedToken)), "live session was sanitized")
	require.NoError(t, s.finish(context.Background()))
}

func TestRecoveryDoesNotResurrectLogoutReturningToPreimage(t *testing.T) {
	for _, stage := range []string{"published:hosts.yml", "applied:hosts.yml"} {
		t.Run(stage, func(t *testing.T) {
			s := journalSession(t)
			writeTestFile(
				t,
				filepath.Join(s.work(), hostsFile),
				authFixture("local-user", "dummy-local-login"),
			)
			s.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			require.Error(t, s.finish(context.Background()))
			require.NoError(t, os.Remove(filepath.Join(s.Original, hostsFile)))
			err := recoverSession(context.Background(), s.dir)
			if err != nil {
				require.ErrorIs(t, err, errAuthConflict)
			}
			require.NoFileExists(t, filepath.Join(s.Original, hostsFile))
		})
	}
}

func TestMalformedWorkingAuthenticationDoesNotDeleteLocalLogin(t *testing.T) {
	s := journalSession(t)
	local := authFixture("local-user", "dummy-local-login")
	writeTestFile(t, filepath.Join(s.Original, hostsFile), local)
	writeTestFile(t, filepath.Join(s.dir, "before", hostsFile), local)
	writeTestFile(t, filepath.Join(s.work(), hostsFile), "[bad "+testForwardedToken)
	require.Error(t, s.finish(context.Background()))
	assertNoForwardedArtifacts(t, s.dir)
	require.NoError(t, recoverSession(context.Background(), s.dir))
	b, err := readFile(filepath.Join(s.Original, hostsFile))
	require.NoError(t, err)
	require.True(t, string(b) == local, "discarded work deleted original authentication")
}

func TestJournalMalformedAuthenticationIsScrubbedBeforeRecovery(t *testing.T) {
	for _, body := range []string{"users: [a, b]", "users:\n    alice: [a, b]", "oauth_token: [a, b]"} {
		t.Run(body, func(t *testing.T) {
			s := journalSession(t)
			local := authFixture("local-user", "dummy-local-login")
			writeTestFile(t, filepath.Join(s.Original, hostsFile), local)
			writeTestFile(t, filepath.Join(s.dir, "before", hostsFile), local)
			writeTestFile(
				t,
				filepath.Join(s.work(), hostsFile),
				"github.com:\n  "+body+"\n  extra: "+testForwardedToken+"\n",
			)
			require.ErrorIs(t, s.finish(context.Background()), errLostIntent)
			assertNoForwardedArtifacts(t, s.dir)
			require.NoError(t, recoverSession(context.Background(), s.dir))
			saved, err := readFile(filepath.Join(s.Original, hostsFile))
			require.NoError(t, err)
			require.True(
				t,
				string(saved) == local,
				"malformed work changed original authentication",
			)
		})
	}
}

func TestJournalMalformedLatestAuthenticationRemainsPendingUntilRepaired(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"structure", "github.com:\n  users: [a, b]\n"},
		{"trailing content", "{}\nignored.test:\n  user: newer-user\n"},
		{"second document", "{}\n---\nignored.test:\n  user: newer-user\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			malformed := tc.data
			s := journalSession(t)
			local := authFixture("local-user", "dummy-local-login")
			writeTestFile(t, filepath.Join(s.work(), hostsFile), local)
			path := filepath.Join(s.Original, hostsFile)
			writeTestFile(t, path, malformed)
			require.ErrorIs(t, s.finish(context.Background()), errInvalidConfig)
			for range 2 {
				assertNoForwardedArtifacts(t, s.dir)
				require.ErrorIs(t, recoverSession(context.Background(), s.dir), errInvalidConfig)
				saved, err := readFile(path)
				require.NoError(t, err)
				require.Equal(t, malformed, string(saved))
			}
			require.NoError(t, os.Remove(path))
			require.NoError(t, recoverSession(context.Background(), s.dir))
			require.NoError(t, recoverSession(context.Background(), s.dir))
			saved, err := readFile(path)
			require.NoError(t, err)
			want, err := decodeHosts([]byte(local))
			require.NoError(t, err)
			got, err := decodeHosts(saved)
			require.NoError(t, err)
			require.True(t, equal(want, got), "recovery changed deliberate local authentication")
			assertNoForwardedArtifacts(t, filepath.Dir(s.dir))
		})
	}
}

func TestJournalPreservesSecureLoginEmptyStringRegistration(t *testing.T) {
	s := journalSession(t)
	working := testHost + ":\n  user: local-user\n  users:\n    " + testUser +
		":\n      oauth_token: " + testForwardedToken + "\n    local-user: \"\"\n"
	writeTestFile(t, filepath.Join(s.work(), hostsFile), working)
	require.NoError(t, s.finish(context.Background()))
	assertNoForwardedArtifacts(t, filepath.Dir(s.dir))
	saved, err := readFile(filepath.Join(s.Original, hostsFile))
	require.NoError(t, err)
	n, err := decodeHosts(saved)
	require.NoError(t, err)
	host := get(n, testHost)
	require.Equal(t, "local-user", get(host, "user").Value)
	require.NotNil(t, get(get(host, "users"), "local-user"))
	a := Account{Host: testHost, User: testUser, Token: testForwardedToken}
	next, err := startSession(context.Background(), s.Original, []Account{a})
	require.NoError(t, err)
	t.Cleanup(func() { _ = next.lock.Close() })
	require.NoError(t, next.inject(context.Background(), "/usr/bin/false", nil))
	require.Empty(t, next.Accounts, "unavailable keyring must preserve the registered guest")
	require.NoError(t, next.finish(context.Background()))
}

func TestJournalTrailingWorkingAuthenticationIsScrubbedWithLostIntent(t *testing.T) {
	for _, prefix := range []string{"{}\n", "{}\n---\n"} {
		t.Run(prefix, func(t *testing.T) {
			s := journalSession(t)
			writeTestFile(
				t,
				filepath.Join(s.work(), hostsFile),
				prefix+authFixture(testUser, testForwardedToken),
			)
			require.ErrorIs(t, s.finish(context.Background()), errLostIntent)
			assertNoForwardedArtifacts(t, s.dir)
			require.NoError(t, recoverSession(context.Background(), s.dir))
			require.NoFileExists(t, filepath.Join(s.Original, hostsFile))
		})
	}
}

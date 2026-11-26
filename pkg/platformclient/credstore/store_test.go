package credstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	keyring "github.com/zalando/go-keyring"
)

const (
	canary      = "canary-value-7f3a91"
	testGateway = "https://gw.example.test"
	looseMode   = os.FileMode(0o644)
)

type fakeKeychain struct {
	value  string
	has    bool
	setErr error
	getErr error
	delErr error
}

func (f *fakeKeychain) Set(value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.value, f.has = value, true
	return nil
}

func (f *fakeKeychain) Get() (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if !f.has {
		return "", errKeychainNotFound
	}
	return f.value, nil
}

func (f *fakeKeychain) Delete() error {
	if f.delErr != nil {
		return f.delErr
	}
	f.value, f.has = "", false
	return nil
}

type StoreSuite struct {
	suite.Suite
	ticks int
	dir   string
	fake  *fakeKeychain
	cred  Credential
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.fake = &fakeKeychain{}
	s.cred = Credential{
		GatewayURL: testGateway,
		Token:      canary,
		ExpiresAt:  time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		UserID:     "u1",
	}
}

func (s *StoreSuite) TestSaveUsesKeychain() {
	warning, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	s.Empty(warning)
	s.True(s.fake.has)
	s.NoFileExists(s.file())
	got, err := s.store().Load()
	s.Require().NoError(err)
	s.Equal(s.cred.Token, got.Token)
	s.Equal(s.cred.GatewayURL, got.GatewayURL)
	s.Equal(s.cred.UserID, got.UserID)
	s.True(got.ExpiresAt.Equal(s.cred.ExpiresAt))
}

func (s *StoreSuite) TestSaveFallsBackToFileWithWarning() {
	s.fake.setErr = errors.New("no secret service")

	warning, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	s.Contains(warning, s.file())
	s.Contains(warning, "0600")
	s.NotContains(warning, canary)
	s.NotContains(warning, "\n")
	s.FileExists(s.file())
	got, err := s.store().Load()
	s.Require().NoError(err)
	s.Equal(canary, got.Token)
}

func (s *StoreSuite) TestFallbackFileHasMode0600() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("file modes are not enforced on Windows")
	}
	s.fake.setErr = errors.New("no secret service")

	_, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	info, err := os.Stat(s.file())
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	s.Len(entries, 1, "no temporary file is left behind")
}

func (s *StoreSuite) TestFallbackReplacesLooseExistingFile() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("file modes are not enforced on Windows")
	}
	s.Require().NoError(os.WriteFile(s.file(), []byte("old"), looseMode))
	s.fake.setErr = errors.New("no secret service")

	_, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	info, err := os.Stat(s.file())
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
}

func (s *StoreSuite) TestSaveToKeychainRemovesStaleFile() {
	s.Require().NoError(os.WriteFile(s.file(), []byte("stale"), 0o600))

	_, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestDeleteClearsKeychainAndFile() {
	s.fake.value, s.fake.has = "x", true
	s.Require().NoError(os.WriteFile(s.file(), []byte("x"), 0o600))

	s.Require().NoError(s.store().Delete())

	s.False(s.fake.has)
	s.NoFileExists(s.file())
	_, err := s.store().Load()
	s.Require().ErrorIs(err, ErrNotLoggedIn)
}

func (s *StoreSuite) TestDeleteWithNothingStoredIsNotAnError() {
	s.Require().NoError(s.store().Delete())
}

func (s *StoreSuite) TestDeleteWithUnavailableKeychainRemovesFileAndReportsIt() {
	s.fake.getErr = errors.New("no secret service")
	s.fake.delErr = errors.New("no secret service")
	s.Require().NoError(os.WriteFile(s.file(), []byte("x"), 0o600))

	err := s.store().Delete()

	s.Require().ErrorIs(err, ErrUnconfirmed)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestLockedKeychainDoesNotLetLogoutHideTheOldLogin() {
	oldLogin := s.cred
	oldLogin.Token = "token-a"
	_, err := s.store().Save(oldLogin)
	s.Require().NoError(err)
	locked := errors.New("keychain locked")
	s.fake.setErr, s.fake.getErr, s.fake.delErr = locked, locked, locked

	warning, err := s.store().Save(s.cred)
	s.Require().NoError(err)
	s.Contains(warning, s.file())
	err = s.store().Delete()

	s.Require().ErrorIs(err, ErrUnconfirmed)
	s.NoFileExists(s.file())
	s.fake.setErr, s.fake.getErr, s.fake.delErr = nil, nil, nil
	got, err := s.store().Load()
	s.Require().NoError(err)
	s.Equal("token-a", got.Token, "the keychain still holds the first login")
}

func (s *StoreSuite) TestLoadReportsUnreadableKeychainWhenNothingElseIsStored() {
	s.fake.getErr = errors.New("locked " + canary)

	_, err := s.store().Load()

	s.Require().ErrorIs(err, ErrKeychainUnreadable)
	s.Require().ErrorIs(err, ErrNotLoggedIn)
	s.NotContains(err.Error(), canary)
}

func (s *StoreSuite) TestLoadUsesFileWhenKeychainIsUnreadable() {
	s.fake.getErr = errors.New("no secret service")
	s.writeCredentialFile(0o600)

	got, err := s.store().Load()

	s.Require().NoError(err)
	s.Equal(canary, got.Token)
}

func (s *StoreSuite) TestSaveToKeychainWarnsWhenOldFileCannotBeRemoved() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.file(), "child"), 0o700))

	warning, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	s.Contains(warning, s.file())
	s.NotContains(warning, canary)
	s.True(s.fake.has)
}

func (s *StoreSuite) TestKeychainIdentityIsPinned() {
	s.Equal("sh.devsy.gateway", Service)
	s.Equal("session", account)
	s.Equal("gateway-credentials.json", fileName)
}

func (s *StoreSuite) TestKeychainValueLimitIs2400Bytes() {
	atLimit := s.credOfEncodedSize(2400)
	_, err := s.store().Save(atLimit)
	s.Require().NoError(err)
	s.True(s.fake.has)
	s.NoFileExists(s.file())

	_, err = s.store().Save(s.credOfEncodedSize(2401))
	s.Require().NoError(err)
	s.False(s.fake.has)
	s.FileExists(s.file())
}

func (s *StoreSuite) TestFileReadLimitIs64KiB() {
	for _, test := range []struct {
		size   int
		loaded bool
	}{{65536, true}, {65537, false}} {
		data, err := json.Marshal(s.credOfEncodedSize(test.size))
		s.Require().NoError(err)
		s.Require().NoError(os.WriteFile(s.file(), data, 0o600))

		_, err = s.store().Load()

		if test.loaded {
			s.Require().NoError(err, test.size)
		} else {
			s.Require().ErrorIs(err, errUnreadable, test.size)
		}
	}
}

func (s *StoreSuite) TestDeleteReportsKeychainFailureAndStillRemovesFile() {
	s.fake.value, s.fake.has = canary, true
	s.fake.delErr = errors.New("denied " + canary)
	s.Require().NoError(os.WriteFile(s.file(), []byte("x"), 0o600))

	err := s.store().Delete()

	s.Require().ErrorIs(err, errStillStored)
	s.NotContains(err.Error(), canary)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestLoadWithNothingStored() {
	_, err := s.store().Load()

	s.Require().ErrorIs(err, ErrNotLoggedIn)
}

func (s *StoreSuite) TestLoadRejectsFileReadableByOthers() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("file modes are not enforced on Windows")
	}
	data := `{"gatewayUrl":"https://gw.example.test","token":"` + canary +
		`","expiresAt":"2030-01-02T03:04:05Z"}`
	s.Require().NoError(os.WriteFile(s.file(), []byte(data), looseMode))
	s.Require().NoError(os.Chmod(s.file(), looseMode))

	_, err := s.store().Load()

	s.Require().Error(err)
	s.Contains(err.Error(), "readable by others")
	s.Contains(err.Error(), s.file())
	s.NotContains(err.Error(), canary)
}

func (s *StoreSuite) TestUnreadableDataIsNotEchoed() {
	for name, payload := range map[string]string{
		"syntax":     "{" + canary,
		"incomplete": `{"gatewayUrl":"https://gw.example.test","token":"` + canary + `"}`,
	} {
		s.Run(name, func() {
			s.Require().NoError(os.WriteFile(s.file(), []byte(payload), 0o600))
			s.fake.value, s.fake.has = payload, false

			_, err := s.store().Load()
			s.Require().ErrorIs(err, errUnreadable)
			s.NotContains(err.Error(), canary)

			s.fake.has = true
			_, err = s.store().Load()
			s.Require().ErrorIs(err, errUnreadable)
			s.NotContains(err.Error(), canary)
		})
	}
}

func (s *StoreSuite) TestSaveRejectsIncompleteCredential() {
	for _, cred := range []Credential{
		{Token: "t", ExpiresAt: s.cred.ExpiresAt},
		{GatewayURL: testGateway, ExpiresAt: s.cred.ExpiresAt},
		{GatewayURL: testGateway, Token: "t"},
	} {
		_, err := s.store().Save(cred)
		s.Require().Error(err)
	}
	s.False(s.fake.has)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestExpired() {
	now := s.cred.ExpiresAt
	s.False(s.cred.Expired(now.Add(-time.Second)))
	s.True(s.cred.Expired(now))
	s.True(s.cred.Expired(now.Add(time.Second)))
}

func (s *StoreSuite) TestSecretStoreEntriesAreUntouched() {
	keyring.MockInit()
	const otherService = "sh.devsy.secrets"
	s.Require().NoError(keyring.Set(otherService, account, "other"))
	store := s.systemStore()

	_, err := store.Save(s.cred)
	s.Require().NoError(err)
	s.Require().NoError(store.Delete())

	got, err := keyring.Get(otherService, account)
	s.Require().NoError(err)
	s.Equal("other", got)
	s.NotEqual(otherService, Service)
}

func (s *StoreSuite) TestSecretsBackendSettingIsIgnored() {
	s.T().Setenv("DEVSY_SECRETS_BACKEND", "file")
	keyring.MockInit()

	warning, err := s.systemStore().Save(s.cred)

	s.Require().NoError(err)
	s.Empty(warning)
	_, err = keyring.Get(Service, account)
	s.Require().NoError(err)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestDeleteWithUnreachableKeychainAndNoFileFails() {
	s.fake.getErr = errors.New("locked " + canary)
	s.fake.delErr = errors.New("locked " + canary)

	err := s.store().Delete()

	s.Require().ErrorIs(err, ErrUnconfirmed)
	s.NotContains(err.Error(), canary)
}

func (s *StoreSuite) TestSaveFailsWhenOldKeychainEntryCannotBeReplaced() {
	s.fake.value, s.fake.has = "old", true
	s.fake.setErr = errors.New("denied")
	s.fake.delErr = errors.New("denied")

	warning, err := s.store().Save(s.cred)

	s.Require().ErrorIs(err, errStale)
	s.Empty(warning)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) TestSaveFallsBackWhenValueIsTooLargeForKeychain() {
	s.cred.Token = strings.Repeat("a", maxKeychainBytes)

	warning, err := s.store().Save(s.cred)

	s.Require().NoError(err)
	s.Contains(warning, s.file())
	s.False(s.fake.has)
	got, err := s.store().Load()
	s.Require().NoError(err)
	s.Equal(s.cred.Token, got.Token)
}

func (s *StoreSuite) TestDeleteRemovesStaleTemporaryFiles() {
	leftover := s.writeTemp(s.dir, time.Hour)

	s.Require().NoError(s.store().Delete())

	s.NoFileExists(leftover)
}

func (s *StoreSuite) TestDeleteKeepsTemporaryFilesJustUnderTheLimit() {
	recent := s.writeTemp(s.dir, 50*time.Second)

	s.Require().NoError(s.store().Delete())

	s.FileExists(recent)
}

func (s *StoreSuite) TestDeleteRemovesTemporaryFilesJustOverTheLimit() {
	old := s.writeTemp(s.dir, 70*time.Second)

	s.Require().NoError(s.store().Delete())

	s.NoFileExists(old)
}

func (s *StoreSuite) TestDeleteRemovesFutureDatedTemporaryFiles() {
	skewed := s.writeTemp(s.dir, -2*time.Hour)

	s.Require().NoError(s.store().Delete())

	s.NoFileExists(skewed)
}

func (s *StoreSuite) TestLoadPrefersNewerFileOverStaleKeychainEntry() {
	s.fake.value, s.fake.has = s.entry("old-token", "2020-01-01T00:00:00Z"), true
	s.fake.setErr = errors.New("locked")
	s.fake.delErr = errors.New("locked")
	s.fake.getErr = errors.New("locked")

	_, err := s.store().Save(s.cred)
	s.Require().NoError(err)
	s.fake.setErr, s.fake.delErr, s.fake.getErr = nil, nil, nil
	got, err := s.store().Load()

	s.Require().NoError(err)
	s.Equal(canary, got.Token)
}

func (s *StoreSuite) TestLoadKeepsKeychainEntryWhenFileIsNotNewer() {
	s.writeCredentialFile(0o600)
	s.fake.value, s.fake.has = s.entry("keychain-token", "2020-01-01T00:00:00Z"), true

	got, err := s.store().Load()

	s.Require().NoError(err)
	s.Equal("keychain-token", got.Token)
}

func (s *StoreSuite) TestLoadKeepsKeychainEntryWhenSavedAtIsEqual() {
	saved := "2020-01-01T00:00:00Z"
	s.fake.value, s.fake.has = s.entry("keychain-token", saved), true
	s.Require().NoError(os.WriteFile(s.file(), []byte(s.entry("file-token", saved)), 0o600))

	got, err := s.store().Load()

	s.Require().NoError(err)
	s.Equal("keychain-token", got.Token)
}

func (s *StoreSuite) TestLoadIgnoresExpiryWhenChoosingBetweenLogins() {
	s.Require().NoError(os.WriteFile(s.file(), []byte(
		`{"gatewayUrl":"https://other.example.test","token":"file-token",`+
			`"expiresAt":"2031-01-02T03:04:05Z","savedAt":"2020-01-01T00:00:00Z"}`,
	), 0o600))
	s.fake.value, s.fake.has = s.entry("keychain-token", "2021-01-01T00:00:00Z"), true

	got, err := s.store().Load()

	s.Require().NoError(err)
	s.Equal("keychain-token", got.Token)
	s.Equal(testGateway, got.GatewayURL)
}

func (s *StoreSuite) TestDeleteRemovesLeftoversInDirectoryWithGlobCharacters() {
	s.dir = filepath.Join(s.dir, "a[b*")
	s.Require().NoError(os.MkdirAll(s.dir, 0o700))
	leftover := s.writeTemp(s.dir, time.Hour)

	s.Require().NoError(s.store().Delete())

	s.NoFileExists(leftover)
}

func (s *StoreSuite) TestLoadRejectsEveryModeWithGroupOrOtherBits() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("file modes are not enforced on Windows")
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o670, 0o606, 0o660} {
		s.Run(mode.String(), func() {
			s.writeCredentialFile(mode)

			_, err := s.store().Load()

			s.Require().Error(err)
			s.Contains(err.Error(), "readable by others")
		})
	}
}

func (s *StoreSuite) TestLoadAcceptsOwnerOnlyModes() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("file modes are not enforced on Windows")
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		s.writeCredentialFile(mode)

		got, err := s.store().Load()

		s.Require().NoError(err)
		s.Equal(canary, got.Token)
	}
}

func (s *StoreSuite) TestLoadRefusesSymlink() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("symlinks need extra rights on Windows")
	}
	target := filepath.Join(s.T().TempDir(), "elsewhere")
	s.Require().NoError(os.WriteFile(target, []byte("{}"), 0o600))
	s.Require().NoError(os.Symlink(target, s.file()))

	_, err := s.store().Load()

	s.Require().Error(err)
	s.Contains(err.Error(), "not a regular file")
}

func (s *StoreSuite) TestCredentialFormattingHidesToken() {
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("login", "credential", s.cred)

	for _, out := range []string{
		fmt.Sprintf("%v", s.cred),
		fmt.Sprintf("%+v", s.cred),
		fmt.Sprintf("%#v", s.cred),
		fmt.Sprint(s.cred),
		fmt.Sprintf("%v", &s.cred),
		logs.String(),
	} {
		s.NotContains(out, canary)
		s.Contains(out, testGateway)
	}
}

func (s *StoreSuite) TestSystemKeychainRoundTrip() {
	keyring.MockInit()
	store := s.systemStore()

	_, err := store.Save(s.cred)
	s.Require().NoError(err)
	got, err := store.Load()
	s.Require().NoError(err)
	s.Equal(canary, got.Token)
	stored, err := keyring.Get(Service, account)
	s.Require().NoError(err)
	s.True(strings.Contains(stored, canary))
	s.Require().NoError(store.Delete())
	_, err = store.Load()
	s.Require().ErrorIs(err, ErrNotLoggedIn)
}

func (s *StoreSuite) TestSystemKeychainFailureFallsBackToFile() {
	keyring.MockInitWithError(errors.New("no secret service"))
	store := s.systemStore()

	warning, err := store.Save(s.cred)

	s.Require().NoError(err)
	s.NotEmpty(warning)
	s.FileExists(s.file())
	got, err := store.Load()
	s.Require().NoError(err)
	s.Equal(canary, got.Token)
	s.Require().ErrorIs(store.Delete(), ErrUnconfirmed)
	s.NoFileExists(s.file())
}

func (s *StoreSuite) store() *Store {
	return &Store{keychain: s.fake, dir: func() (string, error) { return s.dir, nil }, now: s.clock}
}

func (s *StoreSuite) file() string { return filepath.Join(s.dir, fileName) }

func (s *StoreSuite) writeTemp(dir string, age time.Duration) string {
	path := filepath.Join(dir, ".gateway-credentials-123")
	s.Require().NoError(os.WriteFile(path, []byte(canary), 0o600))
	when := time.Now().Add(-age)
	s.Require().NoError(os.Chtimes(path, when, when))
	return path
}

// credOfEncodedSize returns a credential whose JSON form is exactly size bytes.
func (s *StoreSuite) credOfEncodedSize(size int) Credential {
	cred := s.cred
	cred.SavedAt = s.clock()
	base, err := json.Marshal(cred)
	s.Require().NoError(err)
	cred.Token += strings.Repeat("a", size-len(base))
	data, err := json.Marshal(cred)
	s.Require().NoError(err)
	s.Require().Len(data, size)
	return cred
}

// clock returns a time one whole second after the last call, so saved times
// are ordered and always encode to the same length.
func (s *StoreSuite) clock() time.Time {
	s.ticks++
	return time.Date(2029, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(s.ticks) * time.Second)
}

func (s *StoreSuite) entry(token, savedAt string) string {
	return `{"gatewayUrl":"https://gw.example.test","token":"` + token +
		`","expiresAt":"2030-01-02T03:04:05Z","savedAt":"` + savedAt + `"}`
}

func (s *StoreSuite) systemStore() *Store {
	return &Store{
		keychain: systemKeychain{},
		dir:      func() (string, error) { return s.dir, nil },
		now:      s.clock,
	}
}

func (s *StoreSuite) writeCredentialFile(mode os.FileMode) {
	data := `{"gatewayUrl":"https://gw.example.test","token":"` + canary +
		`","expiresAt":"2030-01-02T03:04:05Z"}`
	_ = os.Remove(s.file())
	s.Require().NoError(os.WriteFile(s.file(), []byte(data), 0o600))
	s.Require().NoError(os.Chmod(s.file(), mode))
}

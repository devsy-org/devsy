package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type WSLHelpersSuite struct{ suite.Suite }

func TestWSLHelpersSuite(t *testing.T) { suite.Run(t, new(WSLHelpersSuite)) }

func jsonOf(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data), err
}

func testSpec() sshHostSpec {
	return sshHostSpec{
		RunID: testRunID, Alias: testAlias, HostName: testVMIP, Port: sshPort,
		IdentityFile: `C:\a\_temp\id`, KnownHosts: `C:\a\_temp\known hosts`, HostKeyAlias: true,
	}
}

func (s *WSLHelpersSuite) TestSSHConfigBlockIsStrictAndScoped() {
	block := sshConfigBlock(testSpec())

	for _, want := range []string{
		"Host " + testAlias, "  HostName " + testVMIP, "  User devsyci", "  Port 2222",
		"  IdentitiesOnly yes", "  HostKeyAlias " + testAlias, "  StrictHostKeyChecking yes",
		"  PasswordAuthentication no", "  BatchMode yes",
		"  IdentityFile C:/a/_temp/id", `  UserKnownHostsFile "C:/a/_temp/known hosts"`,
	} {
		s.Contains(block, want)
	}
	s.True(strings.HasPrefix(block, sshBlockBegin(testRunID)))
	s.True(strings.HasSuffix(block, sshBlockEnd(testRunID)))
	s.NotContains(block, "\r")
}

func (s *WSLHelpersSuite) TestDirectModeOmitsHostKeyAlias() {
	spec := testSpec()
	spec.HostKeyAlias, spec.Alias = false, testVMIP
	s.NotContains(sshConfigBlock(spec), "HostKeyAlias")
	s.Equal("ssh://devsyci@"+testVMIP+":2222", spec.endpoint())
	s.Equal("[172.20.1.5]:2222 K\n", knownHostsContent(spec, "K"))
}

func (s *WSLHelpersSuite) TestKnownHostsPinsAliasAndBracketedAlias() {
	s.Equal(testAlias+" K\n["+testAlias+"]:2222 K\n", knownHostsContent(testSpec(), "K"))
	s.Equal("ssh://devsyci@"+testAlias, testSpec().endpoint())
}

func (s *WSLHelpersSuite) TestPrependAndStripRoundTripIsByteExact() {
	block := sshConfigBlock(testSpec())
	for name, original := range map[string][]byte{
		"empty":         nil,
		"crlf":          []byte("Host a\r\n  User x\r\n"),
		"no newline":    []byte("Host a"),
		"binary-ish":    {0x00, 0xff, '\n', 0x80},
		"leading blank": []byte("\n\nHost a\n"),
	} {
		s.Run(name, func() {
			combined := prependSSHBlock(original, block)
			got, ok := stripSSHBlock(combined, testRunID)
			s.True(ok)
			s.Equal(string(original), string(got))
		})
	}
}

func (s *WSLHelpersSuite) TestStripIgnoresOtherRunsAndMangledBlocks() {
	combined := prependSSHBlock([]byte("x"), sshConfigBlock(testSpec()))
	_, ok := stripSSHBlock(combined, "other")
	s.False(ok)
	_, ok = stripSSHBlock([]byte(sshBlockBegin(testRunID)+"Host a\n"), testRunID)
	s.False(ok, "no end marker")
	_, ok = stripSSHBlock([]byte("# user\n"+string(combined)), testRunID)
	s.False(ok, "block no longer first")
}

func (s *WSLHelpersSuite) TestParseHostKey() {
	key, err := parseHostKey(testPublicKey(3, "root@box") + "\n")
	s.Require().NoError(err)
	s.NotContains(key, "root@box")
	s.True(strings.HasPrefix(key, "ssh-ed25519 "))
	for _, bad := range []string{"", "ssh-rsa AAAA", "ssh-ed25519", "ssh-ed25519 !!!", "ssh-ed25519 AAAA"} {
		_, err := parseHostKey(bad)
		s.Error(err, bad)
	}
}

func (s *WSLHelpersSuite) TestKeyACLVerification() {
	const key = `C:\a\id`
	ok := key + ` HOST\runner:(R)` + "\n\nSuccessfully processed 1 files; Failed processing 0 files\n"
	s.Require().NoError(verifyKeyACL(ok, key, "runner"))
	s.Require().NoError(verifyKeyACL(key+` RUNNER:(R)`, key, "runner"), "case-insensitive user")
	for name, out := range map[string]string{
		"extra entry":  ok + ` BUILTIN\Users:(R)` + "\n",
		"inherited":    ok + ` NT AUTHORITY\Authenticated Users:(I)(R)` + "\n",
		"wrong user":   key + ` HOST\someone:(F)`,
		"no entries":   "Successfully processed 1 files\n",
		"empty output": "",
	} {
		s.Error(verifyKeyACL(out, key, "runner"), name)
	}
	s.Equal(
		[]string{key, "/inheritance:r", "/grant:r", "runner:(R)"},
		icaclsRestrictArgs(key, "runner"),
	)
}

func (s *WSLHelpersSuite) TestNormalizeWSLOutput() {
	cases := map[string]string{
		"utf16 bom":     "\xff\xfe" + utf16le("Ubuntu\r\nDebian\r\n"),
		"utf16 no bom":  utf16le("Ubuntu\r\nDebian\r\n"),
		"utf16 odd":     utf16le("Ubuntu\r\nDebian\r\n") + "\x00",
		"utf8 bom crlf": "\xef\xbb\xbfUbuntu\r\nDebian\r\n",
		"utf8":          "Ubuntu\nDebian\n",
	}
	for name, raw := range cases {
		s.Equal([]string{"Ubuntu", "Debian"}, parseWSLDistributions(raw), name)
	}
	s.Empty(parseWSLDistributions(""))
	s.Equal("a\nb", normalizeWSLOutput("a\rb"))
}

func (s *WSLHelpersSuite) TestSanitizeRunID() {
	s.Equal("77-1-e2e", sanitizeRunID("-77-1-e2e"))
	s.Equal("a-b-c", sanitizeRunID("a b/c"))
	s.Equal("x", sanitizeRunID("../x.."), "dots and dashes are trimmed from both ends")
	s.Len(sanitizeRunID(strings.Repeat("a", 200)), maxRunIDLen)
	s.Empty(sanitizeRunID("///"))
}

func (s *WSLHelpersSuite) TestScriptArgvRejectsUnsafeArguments() {
	for _, bad := range []string{"", "a b", `a"b`, "a;b", "$(x)", "a\nb", "a'b"} {
		_, err := wslScript{body: "true", args: []string{bad}}.argv()
		s.Error(err, bad)
	}
	argv, err := wslScript{
		body: "true",
		args: []string{"/run/x.pid", "--pidfile=/x", "devsy-ci:77"},
	}.argv()
	s.Require().NoError(err)
	s.NotContains(argv[2], `"`, "Windows argument quoting must have nothing to escape")
	s.Less(len(argv[2]), 8000)
}

func (s *WSLHelpersSuite) TestEveryScriptFitsTheCommandLine() {
	for _, body := range []string{
		preflightScript, installPackagesScript, stopInstallerDaemonScript, prepareRuntimeScript,
		tcpListenScript, createUserScript, configureSSHDScript, installKeyScript,
		stopOwnedScript, removeUserScript, removeFilesScript, diagnosticsScript,
	} {
		argv, err := wslScript{body: body, args: []string{"x"}}.argv()
		s.Require().NoError(err)
		s.NotContains(argv[2], `"`)
		s.Less(len(argv[2]), 8000, "Windows command lines are limited to 32767 characters")
	}
}

// TestScriptsHonorSecurityRules pins the rules that must never regress: no
// broad pkill, no TCP Docker API, one authoritative dockerd socket, key-only sshd.
func (s *WSLHelpersSuite) TestScriptsHonorSecurityRules() {
	all := strings.Join([]string{
		preflightScript, installPackagesScript, stopInstallerDaemonScript, prepareRuntimeScript,
		tcpListenScript, createUserScript, configureSSHDScript, installKeyScript,
		stopOwnedScript, removeUserScript, removeFilesScript, diagnosticsScript,
		dockerdLaunchScript, sshdLaunchScript,
	}, "\n")
	s.NotRegexp(`pkill\s+(-x|-f)?\s*(dockerd|sshd|docker)`, all)
	s.NotContains(all, "killall")
	s.NotContains(all, "pkill -x")
	s.NotContains(all, "2375", "the Docker API must stay off TCP")
	s.NotContains(dockerdLaunchScript, "tcp://")
	s.NotContains(prepareRuntimeScript, "hosts")
	s.Equal(1, strings.Count(dockerdLaunchScript, "--host="))
	for _, want := range []string{
		"PasswordAuthentication no", "KbdInteractiveAuthentication no", "PubkeyAuthentication yes",
		"PermitRootLogin no", "AllowUsers devsyci", "UsePAM no", "Port 2222",
	} {
		s.Contains(configureSSHDScript, want)
	}
}

// TestScriptsAndGoConstantsAgree keeps the literal paths in the scripts equal to
// the constants cleanup validates against.
func (s *WSLHelpersSuite) TestScriptsAndGoConstantsAgree() {
	s.Contains(configureSSHDScript, "PidFile "+sshdPidfile)
	s.Contains(configureSSHDScript, "HostKey "+sshdConfigDir+"/host_ed25519")
	s.Contains(configureSSHDScript, "dir="+sshdConfigDir)
	s.Contains(removeFilesScript, sshdConfigDir)
	s.Contains(removeFilesScript, dockerRuntimeDir)
	s.Contains(removeFilesScript, linuxLogDir)
	s.Contains(prepareRuntimeScript, dockerRuntimeDir)
	s.Contains(prepareRuntimeScript, linuxLogDir)
	s.Contains(preflightScript, "id -u "+ciUser)
	s.Contains(configureSSHDScript, "AllowUsers "+ciUser)
	s.Contains(configureSSHDScript, "Port "+sshPort)
	s.Contains(dockerdLaunchScript, dockerPidfile)
	s.Contains(dockerdLaunchScript, dockerLogFile)
	s.Contains(sshdLaunchScript, sshdConfigFile)
	s.Contains(sshdLaunchScript, sshdLogFile)
	s.Equal("08AE", strings.ToUpper(sshPortHex))
	s.Equal("0947", dockerTCPPortHex)
}

type WSLStateSuite struct {
	suite.Suite
	guard stateGuard
}

func TestWSLStateSuite(t *testing.T) { suite.Run(t, new(WSLStateSuite)) }

func (s *WSLStateSuite) SetupTest() {
	s.guard = stateGuard{runID: testRunID, stateDir: testStateDir, userConfig: testUserConfig}
}

func (s *WSLStateSuite) Valid() *dockerWSLState {
	st := newDockerWSLState(testRunID, defaultDistribution)
	st.Owned = ownedResources{
		DockerPidfile:      dockerPidfile,
		SSHDPidfile:        sshdPidfile,
		SSHDConfigDir:      sshdConfigDir,
		CIUser:             ciUser,
		SSHIdentityFile:    testFixture + "/id_ed25519",
		KnownHostsFile:     testFixture + "/known_hosts",
		DockerCLIDirectory: testFixture + "/docker-cli",
		DockerBinDirectory: testFixture + "/docker-cli/docker",
		DockerConfigDir:    testFixture + "/docker-config",
		SSHConfig:          testUserConfig,
		SSHConfigBackup:    testFixture + "/ssh-config-original",
	}
	st.SSHHostAlias, st.SSHEndpoint = testAlias, "ssh://devsyci@"+testAlias
	return st
}

func (s *WSLStateSuite) TestValidStatePasses() {
	s.Require().NoError(s.guard.validate(s.Valid()))
	s.Require().NoError(s.guard.validate(newDockerWSLState(testRunID, defaultDistribution)))
}

func (s *WSLStateSuite) TestTamperedStateIsRejected() {
	for name, mutate := range map[string]func(*dockerWSLState){
		"version":          func(st *dockerWSLState) { st.Version = 2 },
		"owner":            func(st *dockerWSLState) { st.Owner = "someone/else" },
		"other run":        func(st *dockerWSLState) { st.RunID = "99-1-e2e" },
		"distribution":     func(st *dockerWSLState) { st.Distribution = "Ubuntu; rm -rf /" },
		"stage":            func(st *dockerWSLState) { st.Stage = "bogus" },
		"alias":            func(st *dockerWSLState) { st.SSHHostAlias = "a b" },
		"endpoint":         func(st *dockerWSLState) { st.SSHEndpoint = "ssh://root@x" },
		"key outside":      func(st *dockerWSLState) { st.Owned.SSHIdentityFile = etcPasswd },
		"key traversal":    func(st *dockerWSLState) { st.Owned.SSHIdentityFile = testFixture + "/../../../etc/passwd" },
		"fixture itself":   func(st *dockerWSLState) { st.Owned.DockerCLIDirectory = testFixture },
		"sibling run":      func(st *dockerWSLState) { st.Owned.DockerConfigDir = "/tmp/rt/devsy-ci/run-other/x" },
		"state dir":        func(st *dockerWSLState) { st.Owned.KnownHostsFile = testStateDir },
		"pidfile":          func(st *dockerWSLState) { st.Owned.DockerPidfile = etcPasswd },
		"sshd config dir":  func(st *dockerWSLState) { st.Owned.SSHDConfigDir = "/etc/ssh" },
		"ci user":          func(st *dockerWSLState) { st.Owned.CIUser = "root" },
		"ssh config other": func(st *dockerWSLState) { st.Owned.SSHConfig = "/etc/ssh/ssh_config" },
		"acl backup":       func(st *dockerWSLState) { st.Owned.SSHConfigACLBackup = "/tmp/elsewhere" },
	} {
		st := s.Valid()
		mutate(st)
		err := s.guard.validate(st)
		s.Require().Error(err, name)
		s.Equal([]Code{CodeConfigInvalid}, codesOf(err), name)
	}
}

func (s *WSLStateSuite) TestStoreRoundTripOnRealFilesIsAtomicAndRestricted() {
	dir := s.T().TempDir()
	store := newStateStore(osFileStore{}, filepath.Join(dir, "nested", "state.json"))
	st := s.Valid()

	s.Require().NoError(store.save(st))
	s.Require().NoError(store.save(st), "replacing an existing file works")
	got, err := store.load()
	s.Require().NoError(err)
	s.Equal(st, got)

	entries, err := filepath.Glob(filepath.Join(dir, "nested", "*"))
	s.Require().NoError(err)
	s.Len(entries, 1, "no temp file may remain beside the state")
	s.Require().NoError(store.remove())
	s.Require().NoError(store.remove(), "removing twice is fine")
	_, err = store.load()
	s.Require().ErrorIs(err, errStateMissing)
}

func (s *WSLStateSuite) TestLoadRejectsMalformedAndUnknownFields() {
	files := newWSLFiles()
	store := newStateStore(files, testStateFile)
	for name, content := range map[string]string{
		"garbage":       "{not json",
		"unknown field": `{"version":1,"owner":"` + dockerWSLOwner + `","surprise":true}`,
		"empty":         "",
	} {
		files.files[testStateFile] = []byte(content)
		_, err := store.load()
		s.Require().Error(err, name)
	}
}

func (s *WSLStateSuite) TestStateNeverHoldsSecrets() {
	data, err := jsonOf(s.Valid())
	s.Require().NoError(err)
	s.NotContains(data, "PRIVATE")
	s.NotContains(strings.ToLower(data), "password")
	s.NotContains(strings.ToLower(data), "token")
}

func (s *WSLStateSuite) TestPathWithin() {
	s.True(pathWithin("/a/b", "/a/b/c"))
	s.True(pathWithin("/a/b", "/a/b/c/d"))
	s.False(pathWithin("/a/b", "/a/b"))
	s.False(pathWithin("/a/b", "/a/bc"))
	s.False(pathWithin("/a/b", "/a/b/../x"))
	s.False(pathWithin("/a/b", "/a"))
}

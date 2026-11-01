package ssh

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	aliasCanonicalHost = "myws.devsy"
	aliasPreferredHost = "friendly.devsy"
	aliasTestExec      = "/path/to/exec"
	aliasTestContext   = "ctx"
	aliasTestUser      = "dev"
)

type SSHAliasTestSuite struct {
	suite.Suite
	home    string
	sshDir  string
	cfgPath string
}

func TestSSHAliasSuite(t *testing.T) {
	suite.Run(t, new(SSHAliasTestSuite))
}

func (s *SSHAliasTestSuite) SetupTest() {
	s.home = s.T().TempDir()
	s.T().Setenv("HOME", s.home)
	s.T().Setenv("USERPROFILE", s.home)
	s.sshDir = filepath.Join(s.home, ".ssh")
	s.Require().NoError(os.MkdirAll(s.sshDir, 0o700))
	s.cfgPath = filepath.Join(s.sshDir, "config")
}

func (s *SSHAliasTestSuite) write(path, content string) {
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
}

func (s *SSHAliasTestSuite) read(path string) string {
	data, err := os.ReadFile(path)
	s.Require().NoError(err)

	return string(data)
}

func (s *SSHAliasTestSuite) managedBlock(host string) string {
	return MarkerStartPrefix + host + "\nHost " + host + "\n  User " + aliasTestUser + "\n" +
		MarkerEndPrefix + host
}

func (s *SSHAliasTestSuite) TestDualHostLineProxyCommand() {
	result, err := addHostSection("", aliasTestExec, addHostParams{
		host:          aliasCanonicalHost,
		preferredHost: aliasPreferredHost,
		user:          aliasTestUser,
		context:       aliasTestContext,
		workspace:     "myws",
	})
	s.Require().NoError(err)

	lines := strings.Split(result, "\n")
	s.Equal(MarkerStartPrefix+aliasCanonicalHost, lines[0])
	s.Equal("Host "+aliasPreferredHost+" "+aliasCanonicalHost, lines[1])
	s.Contains(result, "--user dev myws")
	s.Equal(MarkerEndPrefix+aliasCanonicalHost, lines[len(lines)-1])
}

func (s *SSHAliasTestSuite) TestDualHostLineTunnel() {
	result, err := addHostSection("", aliasTestExec, addHostParams{
		host:          aliasCanonicalHost,
		preferredHost: aliasPreferredHost,
		user:          aliasTestUser,
		tunnelPort:    2222,
	})
	s.Require().NoError(err)

	s.Contains(result, "Host "+aliasPreferredHost+" "+aliasCanonicalHost+"\n")
	s.Contains(result, "Port 2222")
}

func (s *SSHAliasTestSuite) TestHostLineDedupesEmptyAndEqual() {
	s.Equal("Host "+aliasCanonicalHost, hostLine(aliasCanonicalHost, ""))
	s.Equal("Host "+aliasCanonicalHost, hostLine(aliasCanonicalHost, aliasCanonicalHost))
	s.Equal("Host "+aliasCanonicalHost, hostLine(aliasCanonicalHost, "MYWS.devsy"))
}

func (s *SSHAliasTestSuite) TestConfigureWritesPreferredHost() {
	s.Require().NoError(ConfigureSSHConfig(SSHConfigParams{
		SSHConfigPath: s.cfgPath,
		Context:       aliasTestContext,
		Workspace:     "myws",
		User:          aliasTestUser,
		PreferredHost: aliasPreferredHost,
		UID:           "uid-1",
	}))

	s.Contains(s.read(s.cfgPath), "Host "+aliasPreferredHost+" "+aliasCanonicalHost+"\n")
}

func (s *SSHAliasTestSuite) TestExactMarkerMatch() {
	other := s.managedBlock("foo.devsy-other")
	own := s.managedBlock("foo.devsy")
	s.write(s.cfgPath, other+"\n\n"+own+"\n")

	s.Require().NoError(RemoveFromConfig("foo", s.cfgPath, ""))

	result := s.read(s.cfgPath)
	s.Contains(result, other)
	s.NotContains(result, MarkerStartPrefix+"foo.devsy\n")
}

func (s *SSHAliasTestSuite) TestExactMarkerMatchGetUser() {
	s.write(
		s.cfgPath,
		MarkerStartPrefix+"foo.devsy-other\nHost foo.devsy-other\n  User other\n"+
			MarkerEndPrefix+"foo.devsy-other\n",
	)

	user, err := GetUser("foo", s.cfgPath, "")
	s.Require().NoError(err)
	s.Equal("root", user)
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesPreservesOtherLinesAndRestores() {
	original := "Host *\n  IdentityFile ~/.ssh/id\n\n" +
		MarkerStartPrefix + aliasCanonicalHost + "\n" +
		"Host " + aliasCanonicalHost + "\n  User custom\n  ProxyCommand \"x\" y\n" +
		MarkerEndPrefix + aliasCanonicalHost + "\n\nMatch exec \"true\"\n  User cond\n"
	s.write(s.cfgPath, original)

	restore, updated, err := ReplaceHostAliases(
		s.cfgPath, "", aliasCanonicalHost, aliasPreferredHost,
	)
	s.Require().NoError(err)
	s.True(updated)

	expected := strings.Replace(
		original,
		"Host "+aliasCanonicalHost+"\n",
		"Host "+aliasPreferredHost+" "+aliasCanonicalHost+"\n",
		1,
	)
	s.Equal(expected, s.read(s.cfgPath))

	s.Require().NoError(restore())
	s.Equal(original, s.read(s.cfgPath))
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesClearsPreferred() {
	s.write(
		s.cfgPath,
		MarkerStartPrefix+aliasCanonicalHost+"\nHost "+aliasPreferredHost+" "+aliasCanonicalHost+
			"\n  User u\n"+MarkerEndPrefix+aliasCanonicalHost+"\n",
	)

	_, updated, err := ReplaceHostAliases(s.cfgPath, "", aliasCanonicalHost, "")
	s.Require().NoError(err)
	s.True(updated)
	s.Contains(s.read(s.cfgPath), "\nHost "+aliasCanonicalHost+"\n")
	s.NotContains(s.read(s.cfgPath), aliasPreferredHost)
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesIncludePathWins() {
	includePath := filepath.Join(s.sshDir, "devsy_config")
	s.write(s.cfgPath, "Host *\n  User x\n")
	s.write(includePath, s.managedBlock(aliasCanonicalHost)+"\n")

	_, updated, err := ReplaceHostAliases(
		s.cfgPath, includePath, aliasCanonicalHost, aliasPreferredHost,
	)
	s.Require().NoError(err)
	s.True(updated)
	s.Contains(s.read(includePath), "Host "+aliasPreferredHost+" "+aliasCanonicalHost)
	s.Equal("Host *\n  User x\n", s.read(s.cfgPath))
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesNoSection() {
	content := s.managedBlock("other.devsy") + "\n"
	s.write(s.cfgPath, content)

	restore, updated, err := ReplaceHostAliases(
		s.cfgPath, "", aliasCanonicalHost, aliasPreferredHost,
	)
	s.Require().NoError(err)
	s.False(updated)
	s.Require().NotNil(restore)
	s.NoError(restore())
	s.Equal(content, s.read(s.cfgPath))
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesMissingFile() {
	restore, updated, err := ReplaceHostAliases(
		s.cfgPath, "", aliasCanonicalHost, aliasPreferredHost,
	)
	s.Require().NoError(err)
	s.False(updated)
	s.NoError(restore())
	s.NoFileExists(s.cfgPath)
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesIgnoresSimilarMarker() {
	content := s.managedBlock("myws.devsy-other") + "\n"
	s.write(s.cfgPath, content)

	_, updated, err := ReplaceHostAliases(s.cfgPath, "", aliasCanonicalHost, aliasPreferredHost)
	s.Require().NoError(err)
	s.False(updated)
	s.Equal(content, s.read(s.cfgPath))
}

func (s *SSHAliasTestSuite) TestReplaceHostAliasesFollowsSymlinkAndKeepsMode() {
	if runtime.GOOS == windowsGOOS {
		s.T().Skip("symlink and mode semantics differ on windows")
	}

	realPath := filepath.Join(s.home, "real_config")
	s.write(realPath, s.managedBlock(aliasCanonicalHost)+"\n")
	s.Require().NoError(os.Chmod(realPath, 0o640))
	s.Require().NoError(os.Symlink(realPath, s.cfgPath))

	_, updated, err := ReplaceHostAliases(s.cfgPath, "", aliasCanonicalHost, aliasPreferredHost)
	s.Require().NoError(err)
	s.True(updated)

	info, err := os.Lstat(s.cfgPath)
	s.Require().NoError(err)
	s.NotZero(info.Mode() & os.ModeSymlink)
	s.Contains(s.read(realPath), "Host "+aliasPreferredHost+" "+aliasCanonicalHost)

	realInfo, err := os.Stat(realPath)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o640), realInfo.Mode().Perm())
}

func (s *SSHAliasTestSuite) TestFindHostConflictsExcludesOwnBlock() {
	s.write(
		s.cfgPath,
		s.managedBlock(aliasCanonicalHost)+"\n"+
			"Host "+aliasPreferredHost+"\n  User x\n",
	)

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost, aliasCanonicalHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Equal([]HostConflict{{Host: aliasPreferredHost, File: s.cfgPath}}, conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsOwnPreferredInsideOwnBlock() {
	s.write(
		s.cfgPath,
		MarkerStartPrefix+aliasCanonicalHost+"\nHost "+aliasPreferredHost+" "+aliasCanonicalHost+
			"\n  User u\n"+MarkerEndPrefix+aliasCanonicalHost+"\n",
	)

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Empty(conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsIgnoresWildcards() {
	s.write(s.cfgPath, "Host *\n  User x\nHost *.devsy friendly.*\n  User y\nHost ?riendly.devsy\n")

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Empty(conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsCaseInsensitiveMultiToken() {
	s.write(s.cfgPath, "host Other FRIENDLY.DEVSY\n  User x\n")

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Equal([]HostConflict{{Host: aliasPreferredHost, File: s.cfgPath}}, conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsTraversesIncludes() {
	confD := filepath.Join(s.sshDir, "conf.d")
	absInclude := filepath.Join(s.home, "elsewhere", "extra")
	s.write(s.cfgPath, "Include conf.d/*\nInclude ~/elsewhere/extra\nInclude missing/*\n")
	s.write(filepath.Join(confD, "a"), "Host unrelated\n")
	s.write(filepath.Join(confD, "b"), "Host "+aliasPreferredHost+"\n")
	s.write(absInclude, "Host second.devsy\n")

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost, "second.devsy"}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.ElementsMatch([]HostConflict{
		{Host: aliasPreferredHost, File: filepath.Join(confD, "b")},
		{Host: "second.devsy", File: absInclude},
	}, conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsScansIncludeTargetPath() {
	includePath := filepath.Join(s.sshDir, "devsy_config")
	s.write(s.cfgPath, "Host a\n")
	s.write(includePath, "Host "+aliasPreferredHost+"\n")

	conflicts, err := FindHostConflicts(
		s.cfgPath, includePath, []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Equal([]HostConflict{{Host: aliasPreferredHost, File: includePath}}, conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsMissingFiles() {
	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Empty(conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsIncludeCycleTerminates() {
	other := filepath.Join(s.sshDir, "loop")
	s.write(s.cfgPath, "Include loop\n")
	s.write(other, "Include config\nHost "+aliasPreferredHost+"\n")

	conflicts, err := FindHostConflicts(
		s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost,
	)
	s.Require().NoError(err)
	s.Equal([]HostConflict{{Host: aliasPreferredHost, File: other}}, conflicts)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsDepthLimit() {
	for i := 0; i <= maxIncludeDepth+1; i++ {
		name := filepath.Join(s.sshDir, "chain"+string(rune('a'+i)))
		next := "chain" + string(rune('a'+i+1))
		s.write(name, "Include "+next+"\n")
	}
	s.write(s.cfgPath, "Include chaina\n")

	_, err := FindHostConflicts(s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost)
	s.Error(err)
}

func (s *SSHAliasTestSuite) TestFindHostConflictsUnreadableIncludeFails() {
	if runtime.GOOS == windowsGOOS || os.Geteuid() == 0 {
		s.T().Skip("file permissions are not enforced here")
	}

	locked := filepath.Join(s.sshDir, "locked")
	s.write(locked, "Host x\n")
	s.Require().NoError(os.Chmod(locked, 0o000))
	s.T().Cleanup(func() { _ = os.Chmod(locked, 0o600) })
	s.write(s.cfgPath, "Include locked\n")

	_, err := FindHostConflicts(s.cfgPath, "", []string{aliasPreferredHost}, aliasCanonicalHost)
	s.Error(err)
}

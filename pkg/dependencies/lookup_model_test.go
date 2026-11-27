package dependencies

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/suite"
)

const (
	testDocker    = "docker"
	testDockerExe = "docker.exe"
	testRelative  = "relative"
	testPathExt   = "PATHEXT"
	testMiseDir   = "MISE_DATA_DIR"
	windowsRoot   = `C:\work`
)

type lookupModelSuite struct {
	suite.Suite
	root string
}

func TestLookupModel(t *testing.T) { suite.Run(t, new(lookupModelSuite)) }

func (s *lookupModelSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *lookupModelSuite) TestEnvironmentRequiresAbsoluteWorkingDirectory() {
	for _, cwd := range []string{"", testRelative, "." + string(filepath.Separator)} {
		_, err := NewLookupEnvironment(nil, cwd)
		s.Require().Error(err, cwd)
	}
	_, err := NewLookupEnvironment(nil, s.root+"\x00")
	s.Require().Error(err)
}

func (s *lookupModelSuite) TestWindowsWorkingDirectoryMustBeLocal() {
	for _, cwd := range []string{
		"", "work", `C:work`, `\work`, `/work`, `\\host\share\x`, `\\.\pipe\x`,
		`\\?\UNC\host\share\x`, `\\?\GLOBALROOT\x`, `\\?\C:/work`, "C:\\work\x00",
	} {
		_, err := newLookupEnvironment(nil, cwd, true)
		s.Require().Error(err, cwd)
	}
	for _, cwd := range []string{`C:\work`, `c:/work`, `\\?\C:\work`, `D:\`} {
		_, err := newLookupEnvironment(nil, cwd, true)
		s.Require().NoError(err, cwd)
	}
}

func (s *lookupModelSuite) TestEnvironmentIsASnapshot() {
	values := map[string]string{"PATH": "/one", "EMPTY": ""}
	environment, err := NewLookupEnvironment(values, s.root)
	s.Require().NoError(err)
	values["PATH"] = "/two"
	delete(values, "EMPTY")

	value, present := environment.Value("PATH")
	s.True(present)
	s.Equal("/one", value)
	value, present = environment.Value("EMPTY")
	s.True(present)
	s.Empty(value)
	_, present = environment.Value("ABSENT")
	s.False(present)
	s.Equal(s.root, environment.WorkingDirectory())
}

func (s *lookupModelSuite) TestEnvironmentNeverReadsTheProcess() {
	s.T().Setenv("LOOKUP_MODEL_AMBIENT", "set")
	environment, err := NewLookupEnvironment(map[string]string{}, s.root)
	s.Require().NoError(err)

	_, present := environment.Value("LOOKUP_MODEL_AMBIENT")

	s.False(present)
}

func (s *lookupModelSuite) TestWindowsKeysIgnoreCaseAndRejectDuplicates() {
	environment, err := newLookupEnvironment(map[string]string{"Path": "x"}, windowsRoot, true)
	s.Require().NoError(err)
	value, present := environment.Value("PATH")
	s.True(present)
	s.Equal("x", value)
	_, err = newLookupEnvironment(map[string]string{"Path": "x", "PATH": "y"}, windowsRoot, true)
	s.Require().Error(err)
}

func (s *lookupModelSuite) TestRelativePathsResolveAgainstTheSnapshotDirectory() {
	environment := s.environment(nil)
	absolute := filepath.Join(s.root, "tool")
	separator := string(filepath.Separator)

	got, ok := environment.absolute("sub" + separator + "tool")
	s.True(ok)
	s.Equal(s.root+separator+"sub"+separator+"tool", got)
	got, ok = environment.absolute(absolute)
	s.True(ok)
	s.Equal(absolute, got)
}

func (s *lookupModelSuite) TestRelativePathsKeepTraversalText() {
	environment := s.environment(nil)
	link := filepath.Join("link", "..", "tool")

	got, ok := environment.absolute(link)

	s.True(ok)
	s.Equal(s.root+string(filepath.Separator)+link, got)
}

func (s *lookupModelSuite) TestAbsoluteRefusesEmptyAndZeroValue() {
	_, ok := s.environment(nil).absolute("")
	s.False(ok)
	_, ok = LookupEnvironment{}.absolute("tool")
	s.False(ok)
	_, ok = s.environment(nil).absolute("a\x00b")
	s.False(ok)
}

func (s *lookupModelSuite) TestAbsoluteJoinsWithoutDoubleSeparators() {
	separator := string(filepath.Separator)
	environment := s.environment(nil)
	environment.cwd = s.root + separator
	got, ok := environment.absolute("tool")
	s.True(ok)
	s.Equal(s.root+separator+"tool", got)
	if runtime.GOOS != lookupWindows {
		environment.cwd = "/"
		got, ok = environment.absolute("tool")
		s.True(ok)
		s.Equal("/tool", got)
	}
}

func (s *lookupModelSuite) TestWindowsAbsoluteRules() {
	environment := s.windowsEnvironment(nil)
	for _, path := range []string{
		`C:tool.exe`, `\tool.exe`, `/tool.exe`, `\\host\share\tool.exe`, `\\.\pipe\x`,
		`\\?\UNC\host\share\tool.exe`, `\\?\GLOBALROOT\tool.exe`,
	} {
		_, ok := environment.absolute(path)
		s.False(ok, path)
	}
	got, ok := environment.absolute(`sub\tool.exe`)
	s.True(ok)
	s.Equal(`C:\work\sub\tool.exe`, got)
	got, ok = environment.absolute(`D:\tools\tool.exe`)
	s.True(ok)
	s.Equal(`D:\tools\tool.exe`, got)
	root, err := newLookupEnvironment(nil, `D:\`, true)
	s.Require().NoError(err)
	got, ok = root.absolute("tool.exe")
	s.True(ok)
	s.Equal(`D:\tool.exe`, got)
}

func (s *lookupModelSuite) TestInvocationPathAndCommandName() {
	separator := string(filepath.Separator)
	s.True(invocationPath("bin"+separator+testDocker, false) || runtime.GOOS == lookupWindows)
	s.False(invocationPath(testDocker, false))
	s.True(invocationPath(`C:tool`, true))
	s.True(invocationPath(`a\b`, true))
	for _, command := range []string{"", ".", "..", "dock\x00er", "bin/docker"} {
		s.False(commandName(command, false), command)
	}
	s.True(commandName(testDocker, false))
	s.True(commandName("my tool", false))
}

func (s *lookupModelSuite) TestWindowsCommandNames() {
	for _, command := range []string{
		"CON", "con", "nul.exe", "AUX.txt", "prn", "COM1", "com9.exe", "LPT3", "lpt1.cmd",
		"tool.", "tool ", "to*l", "to?l", "to<l", "to>l", "to|l", "to\"l", "to\x01l", `a\b`, `C:x`,
	} {
		s.False(commandName(command, true), command)
	}
	for _, command := range []string{"docker", testDockerExe, "python3.12", "CONSOLE", "COMX", "com10", "my tool"} {
		s.True(commandName(command, true), command)
	}
}

func (s *lookupModelSuite) TestNativeExecutableNames() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("covered by the Windows rules")
	}
	s.Equal([]string{testDocker}, s.environment(nil).executableNames(testDocker))
}

func (s *lookupModelSuite) TestNativeExecutableBits() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("execute bits are not used on Windows")
	}
	environment := s.environment(nil)
	for mode, executable := range map[os.FileMode]bool{
		0o755: true, 0o700: true, 0o610: true, 0o601: true, 0o644: false, 0o600: false,
	} {
		path := filepath.Join(s.root, "tool")
		s.Require().NoError(os.WriteFile(path, nil, 0o600))
		s.Require().NoError(os.Chmod(path, mode))
		info, err := os.Lstat(path)
		s.Require().NoError(err)

		s.Equal(executable, environment.executableFile(path, info), mode.String())
	}
}

func (s *lookupModelSuite) TestWindowsExecutableNames() {
	environment := s.windowsEnvironment(nil)
	s.Equal([]string{testDockerExe}, environment.executableNames(testDockerExe))
	s.Equal([]string{"docker.EXE"}, s.windowsEnvironment(nil).executableNames("docker.EXE"))
	s.Equal(
		[]string{"docker.COM", "docker.EXE", "docker.BAT", "docker.CMD"},
		environment.executableNames(testDocker),
	)
	s.Equal(
		[]string{"python3.12.COM", "python3.12.EXE", "python3.12.BAT", "python3.12.CMD"},
		environment.executableNames("python3.12"),
	)
}

func (s *lookupModelSuite) TestWindowsPathExtEntriesAreFiltered() {
	custom := s.windowsEnvironment(map[string]string{
		testPathExt: `exe;;.cmd;.a\b;.c:d;.;..;.exe.;.a b;. ;.x*;.p"q;.k` + "\x00" + `;.t` + "\x01",
	})
	s.Equal([]string{testDockerExe, "docker.cmd"}, custom.executableNames(testDocker))
	empty := s.windowsEnvironment(map[string]string{testPathExt: ""})
	defaults := s.windowsEnvironment(nil).executableNames(testDocker)
	s.Equal(defaults, empty.executableNames(testDocker))
	s.Equal([]string{testDockerExe}, empty.executableNames(testDockerExe))
	blank := s.windowsEnvironment(map[string]string{testPathExt: ";.x*"})
	s.Empty(blank.executableNames(testDocker))
}

func (s *lookupModelSuite) TestWindowsExecutableSuffixMustBePathExt() {
	environment := s.windowsEnvironment(nil)
	for _, path := range []string{
		`C:\t\tool.exe`, `C:\t\tool.EXE`, `C:\t\tool.cmd`, `C:\t\tool.bat`, `C:\t\tool.com`,
	} {
		s.True(environment.executableFile(path, fileInfo(0o644)), path)
	}
	for _, path := range []string{
		`C:\t\tool.txt`, `C:\t\tool.dll`, `C:\t\tool`, `C:\t\tool.`, `C:\t\tool..`,
		`C:\t\tool.exe:stream`, `C:\a.exe\tool`, `C:\t\tool.ps1`,
	} {
		s.False(environment.executableFile(path, fileInfo(0o644)), path)
	}
	custom := s.windowsEnvironment(map[string]string{testPathExt: ".PS1"})
	s.True(custom.executableFile(`C:\t\tool.ps1`, fileInfo(0o644)))
	s.False(custom.executableFile(`C:\t\tool.exe`, fileInfo(0o644)))
}

func (s *lookupModelSuite) TestExecutableFileNeedsARegularFile() {
	windows := s.windowsEnvironment(nil)
	native := s.environment(nil)
	for name, info := range map[string]fs.FileInfo{
		"nil": nil, "directory": fileInfo(fs.ModeDir | 0o755),
		"symlink": fileInfo(fs.ModeSymlink | 0o777), "device": fileInfo(fs.ModeDevice | 0o755),
	} {
		s.False(windows.executableFile(`C:\t\tool.exe`, info), name)
		s.False(native.executableFile("/t/tool", info), name)
	}
	s.True(native.executableFile("/t/tool", fileInfo(0o755)))
}

func (s *lookupModelSuite) TestWindowsPathTextMustMeanItsLexicalPath() {
	environment := s.windowsEnvironment(nil)
	for _, path := range []string{
		`C:\x.\tool.exe`, `C:\x \tool.exe`, `C:\x\to*l.exe`, `C:\x\a?.exe`, `C:\x\a|b.exe`,
		`C:\x\a<b>.exe`, `C:\x\a"b.exe`, "C:\\x\\a\x01.exe", `bin.\tool.exe`, `C:\x\tool.exe.`,
	} {
		_, ok := environment.absolute(path)
		s.False(ok, path)
	}
	for _, path := range []string{`C:\x\..\tool.exe`, `C:\x\.\tool.exe`, `\\?\C:\x\a.b`} {
		_, ok := environment.absolute(path)
		s.True(ok, path)
	}
}

func (s *lookupModelSuite) TestIndirectLauncherTreatsDotComponentsAsLaunchers() {
	tools := filepath.Join(s.root, "tools")
	environment := s.environment(map[string]string{testMiseDir: tools})
	s.True(environment.indirectLauncher(tools + "/shims/../shims/" + testDocker))
	s.True(environment.indirectLauncher(tools + "/./shims/" + testDocker))
	s.True(environment.indirectLauncher(tools + "/x/../" + testDocker))
	windows := s.windowsEnvironment(map[string]string{testMiseDir: `C:\d`})
	s.True(windows.indirectLauncher(`C:\d\.\shims\x.exe`))
	s.True(windows.indirectLauncher(`C:\d\x\..\shims\x.exe`))
	s.True(windows.indirectLauncher(`C:/d/./shims/x.exe`))
	s.False(windows.indirectLauncher(`C:\d\x.y\x.exe`))
}

func (s *lookupModelSuite) TestScriptSuffixes() {
	for _, path := range []string{"a.cmd", "a.BAT", "dir/a.Ps1"} {
		s.True(s.environment(nil).scriptPath(path), path)
	}
	for _, path := range []string{"a", "a.exe", "a.sh", "cmd", "dir.cmd/a"} {
		s.False(s.environment(nil).scriptPath(path), path)
	}
	windows := s.windowsEnvironment(nil)
	s.True(windows.scriptPath(`C:\t\a.CMD`))
	s.False(windows.scriptPath(`C:\a.cmd\tool`))
}

func (s *lookupModelSuite) TestIndirectLaunchers() {
	shims := filepath.Join(s.root, "tools", "shims")
	environment := s.environment(map[string]string{
		testMiseDir:     filepath.Join(s.root, "tools"),
		"ASDF_DATA_DIR": testRelative,
	})

	s.True(environment.indirectLauncher(filepath.Join(shims, testDocker)))
	s.False(environment.indirectLauncher(filepath.Join(s.root, "tools", testDocker)))
	s.False(environment.indirectLauncher(filepath.Join(s.root, testRelative, "shims", testDocker)))
	s.False(environment.indirectLauncher(filepath.Join(s.root, "tools", "shims2", testDocker)))
	for _, structural := range []string{
		"/home/u/.local/share/mise/shims/docker",
		"/home/u/.asdf/shims/docker",
		"/home/u/.local/share/aquaproj-aqua/bin/docker",
	} {
		s.True(s.environment(nil).indirectLauncher(filepath.FromSlash(structural)), structural)
	}
	s.False(s.environment(nil).indirectLauncher(filepath.Join(s.root, "bin", testDocker)))
}

func (s *lookupModelSuite) TestWindowsReservedNamesAndStreamsInPaths() {
	environment := s.windowsEnvironment(nil)
	for _, path := range []string{
		`C:\x\NUL`, `C:\x\nul.txt`, `C:\CON\x`, `C:/x/aux.exe`, `C:\x\COM¹`, `C:\x\lpt².cmd`,
		`C:\x\CONIN$`, `C:\x\conout$.exe`, `C:\x\a:b`, `C:\x\a.exe:s`, `\\?\C:\x\a:b`,
		`sub\aux.exe`, `NUL`, `..\COM1`, `a:b`,
	} {
		_, ok := environment.absolute(path)
		s.False(ok, path)
	}
	for _, command := range []string{"CONIN$", "CONOUT$", "COM¹", "lpt³.exe"} {
		s.False(commandName(command, true), command)
	}
	for _, path := range []string{`C:\x\NULL.exe`, `C:\x\COM0`, `C:\x\com10`, `\\?\C:\x\NUL`} {
		_, ok := environment.absolute(path)
		s.True(ok, path)
	}
}

func (s *lookupModelSuite) TestWindowsVerbatimWorkingDirectoryKeepsLexicalMeaning() {
	environment, err := newLookupEnvironment(nil, `\\?\C:\x`, true)
	s.Require().NoError(err)
	for _, path := range []string{"bin/tool.exe", `..\tool.exe`, `bin\.\tool.exe`} {
		_, ok := environment.absolute(path)
		s.False(ok, path)
	}
	got, ok := environment.absolute(`bin\tool.exe`)
	s.True(ok)
	s.Equal(`\\?\C:\x\bin\tool.exe`, got)
}

func (s *lookupModelSuite) TestDirectoryOfRootFile() {
	s.Equal("/", s.environment(nil).directory("/tool"))
	s.Equal("/a", s.environment(nil).directory("/a/tool"))
	s.Equal(`C:\`, s.windowsEnvironment(nil).directory(`C:\tool.exe`))
	s.Equal(`\\?\C:\`, s.windowsEnvironment(nil).directory(`\\?\C:\tool.exe`))
	s.Equal(`C:\a`, s.windowsEnvironment(nil).directory(`C:\a\tool.exe`))
	s.Equal("x:", s.environment(nil).directory("x:/tool"))
	s.Equal("/a:", s.environment(nil).directory("/a:/tool"))
}

func fileInfo(mode fs.FileMode) fs.FileInfo {
	info, err := fstest.MapFS{"f": &fstest.MapFile{Mode: mode}}.Stat("f")
	if err != nil {
		panic(err)
	}
	return info
}

func (s *lookupModelSuite) TestIndirectLaunchersIgnoreExtraSeparators() {
	tools := filepath.Join(s.root, "tools")
	environment := s.environment(map[string]string{testMiseDir: tools + "/"})
	s.True(environment.indirectLauncher(filepath.Join(tools, "shims", testDocker)))
	s.True(environment.indirectLauncher(tools + "//shims//" + testDocker))
	windows := s.windowsEnvironment(map[string]string{testMiseDir: `C:\d\`})
	s.True(windows.indirectLauncher(`C:\d\shims\x.exe`))
	s.True(windows.indirectLauncher(`C:\d\\shims\\x.exe`))
	s.True(windows.indirectLauncher(`\\?\C:\d\shims\x.exe`))
	s.False(windows.indirectLauncher(`C:\d\x.exe`))
}

func (s *lookupModelSuite) TestWindowsIndirectLaunchers() {
	environment := s.windowsEnvironment(map[string]string{
		"mise_data_dir": `C:/Tools/Mise`,
		"AQUA_ROOT_DIR": `relative`,
	})

	s.True(environment.indirectLauncher(`c:\tools\mise\SHIMS\docker.exe`))
	s.True(environment.indirectLauncher(`C:\Tools\Mise\shims\docker.cmd`))
	s.True(environment.indirectLauncher(`C:\Users\u\.asdf\shims\docker.exe`))
	s.True(environment.indirectLauncher(`C:\Users\U\.Local\Share\Mise\Shims\docker.exe`))
	s.False(environment.indirectLauncher(`C:\Tools\Mise\docker.exe`))
	s.False(environment.indirectLauncher(`C:\relative\bin\docker.exe`))
}

func (s *lookupModelSuite) TestWindowsDefaultLauncherLocations() {
	environment := s.windowsEnvironment(nil)
	for _, path := range []string{
		`C:\Users\u\AppData\Local\mise\shims\node.exe`,
		`c:\users\U\appdata\local\MISE\Shims\node.cmd`,
		`C:\Users\u\AppData\Local\aquaproj-aqua\bin\aqua.exe`,
		`C:\Users\u\AppData\Local\Volta\bin\node.exe`,
		`C:\Program Files\Volta\node.exe`,
		`C:\Users\u\scoop\shims\git.exe`,
		`C:\ProgramData\scoop\shims\git.exe`,
		`C:\Users\u\.proto\shims\node.exe`,
		`C:\Users\u\.proto\bin\node.exe`,
		`C:\Users\u\.volta\bin\node.exe`,
	} {
		s.True(environment.indirectLauncher(path), path)
	}
	for _, path := range []string{
		`C:\Users\u\AppData\Local\mise\node.exe`,
		`C:\Users\u\AppData\Local\Programs\tool\tool.exe`,
		`C:\Users\u\scoop\apps\git\current\git.exe`,
		`C:\Program Files\Volta2\node.exe`,
		`C:\Users\u\AppData\Roaming\mise\shims2\node.exe`,
	} {
		s.False(environment.indirectLauncher(path), path)
	}
}

func (s *lookupModelSuite) TestWindowsLauncherRootsFromTheEnvironment() {
	for key, paths := range map[string][]string{
		"LOCALAPPDATA": {`D:\L\mise\shims\x.exe`, `D:\L\aquaproj-aqua\bin\x.exe`, `D:\L\Volta\bin\x.exe`},
		"VOLTA_HOME":   {`D:\L\bin\x.exe`},
		"PROTO_HOME":   {`D:\L\shims\x.exe`, `D:\L\bin\x.exe`},
		"SCOOP":        {`D:\L\shims\x.exe`},
		"SCOOP_GLOBAL": {`D:\L\shims\x.exe`},
	} {
		environment := s.windowsEnvironment(map[string]string{key: `D:\L`})
		for _, path := range paths {
			s.True(environment.indirectLauncher(path), key+" "+path)
		}
		s.False(environment.indirectLauncher(`D:\L\x.exe`), key)
	}
}

func (s *lookupModelSuite) TestPosixDefaultLauncherLocations() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("covered by the Windows rules")
	}
	environment := s.environment(nil)
	for _, path := range []string{
		"/home/u/.volta/bin/node", "/home/u/.proto/shims/node", "/home/u/.proto/bin/node",
	} {
		s.True(environment.indirectLauncher(path), path)
	}
	for _, path := range []string{
		"/home/u/AppData/Local/mise/shims/node", "/home/u/scoop/shims/git", "/opt/Volta/node",
	} {
		s.False(environment.indirectLauncher(path), path)
	}
}

func (s *lookupModelSuite) TestLauncherRootsAreCleanedLexically() {
	windows := s.windowsEnvironment(map[string]string{testMiseDir: `C:\d\..\e`})
	s.True(windows.indirectLauncher(`C:\e\shims\x.exe`))
	s.False(windows.indirectLauncher(`C:\d\shims\x.exe`))
	rooted := s.windowsEnvironment(map[string]string{testMiseDir: `C:\..\e`})
	s.True(rooted.indirectLauncher(`C:\e\shims\x.exe`))
	if runtime.GOOS == lookupWindows {
		return
	}
	tools := filepath.Join(s.root, "tools")
	native := s.environment(map[string]string{testMiseDir: tools + "/x/../y"})
	s.True(native.indirectLauncher(tools + "/y/shims/docker"))
	s.False(native.indirectLauncher(tools + "/x/shims/docker"))
}

func (s *lookupModelSuite) environment(values map[string]string) LookupEnvironment {
	environment, err := NewLookupEnvironment(values, s.root)
	s.Require().NoError(err)
	return environment
}

func (s *lookupModelSuite) windowsEnvironment(values map[string]string) LookupEnvironment {
	environment, err := newLookupEnvironment(values, windowsRoot, true)
	s.Require().NoError(err)
	return environment
}

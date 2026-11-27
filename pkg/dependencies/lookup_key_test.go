package dependencies

const (
	deviceName         = `C:\w\CON.exe`
	verbatimDeviceName = `\\?\C:\w\CON.exe`
	plainFile          = `C:\x\a.exe`
	verbatimFile       = `\\?\C:\x\a.exe`
	rootFile           = `C:\a.exe`
	parentFile         = `C:\x\..\a.exe`
)

func (s *lookupModelSuite) TestLexicalKeyWindowsSpellingsOfOneFileShareAKey() {
	environment := s.windowsEnvironment(nil)
	same := [][2]string{
		{plainFile, `c:\X\A.EXE`},
		{plainFile, `C:/x/a.exe`},
		{plainFile, `C:/x\a.exe`},
		{plainFile, `C:\x/a.exe`},
		{plainFile, `C:\x\\a.exe`},
		{plainFile, `C:\\x//a.exe`},
		{verbatimFile, `\\?\c:\X\A.EXE`},
	}
	for _, pair := range same {
		s.Equal(
			environment.lexicalKey(pair[0]),
			environment.lexicalKey(pair[1]),
			pair[0]+" "+pair[1],
		)
	}
}

func (s *lookupModelSuite) TestLexicalKeyWindowsKeepsDistinctFilesApart() {
	environment := s.windowsEnvironment(nil)
	different := [][2]string{
		{plainFile, `C:\y\a.exe`},
		{plainFile, `C:\x\b.exe`},
		{parentFile, rootFile},
		{`C:\x/..\a.exe`, rootFile},
		{`C:/x/../a.exe`, rootFile},
		{`C:\x\.\a.exe`, plainFile},
		{`C:\x/./a.exe`, plainFile},
		{`C:\x\.\a.exe`, parentFile},
		{parentFile, `C:\y\..\a.exe`},
	}
	for _, pair := range different {
		s.NotEqual(
			environment.lexicalKey(pair[0]),
			environment.lexicalKey(pair[1]),
			pair[0]+" "+pair[1],
		)
	}
}

// A verbatim path is not normalized by Win32, so it never shares a key with an
// ordinary spelling, even when the text looks equivalent.
func (s *lookupModelSuite) TestLexicalKeyWindowsVerbatimNeverSharesAnOrdinaryKey() {
	environment := s.windowsEnvironment(nil)
	for _, pair := range [][2]string{
		{verbatimFile, plainFile},
		{`\\?\C:\x\\a.exe`, plainFile},
		{`\\?\C:\x\\a.exe`, verbatimFile},
		{`\\?\C:\x/a.exe`, verbatimFile},
		{`\\?\C:\x\..\a.exe`, `\\?\C:\a.exe`},
	} {
		s.NotEqual(
			environment.lexicalKey(pair[0]),
			environment.lexicalKey(pair[1]),
			pair[0]+" "+pair[1],
		)
	}
}

func (s *lookupModelSuite) TestRefusedWindowsPathDoesNotHideAVerbatimFile() {
	environment := s.windowsEnvironment(nil)
	for _, paths := range [][]string{
		{deviceName, verbatimDeviceName},
		{verbatimDeviceName, deviceName},
	} {
		var result LookupResult
		for _, path := range paths {
			result.append(
				environment,
				ExecutableCandidate{LexicalPath: path, State: FileExecutable},
			)
		}

		s.Len(result.Candidates, 2, paths)
	}
}

func (s *lookupModelSuite) TestLexicalKeyUnixTreatsBackslashAsAnOrdinaryCharacter() {
	environment := s.environment(nil)
	s.Equal(
		environment.lexicalKey(`/t/a\..\b`), environment.lexicalKey(`/t//a\..\b`),
		"a backslash is not a separator, so there is no dot component",
	)
	s.NotEqual(environment.lexicalKey(`/t/a\b`), environment.lexicalKey("/t/a/b"))
	s.NotEqual(environment.lexicalKey("/t/./a"), environment.lexicalKey("/t/a"))
	s.NotEqual(environment.lexicalKey("/t/../a"), environment.lexicalKey("/a"))
	s.Equal(environment.lexicalKey("/t/a"), environment.lexicalKey("/t//a"))
}

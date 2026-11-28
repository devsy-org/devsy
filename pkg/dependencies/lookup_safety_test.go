package dependencies

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

const (
	walkLocalVolume = `\Device\HarddiskVolume7`
	walkSafeFile    = `C:\safe\bin\docker.exe`
	walkStat        = "stat"
	walkSteps       = 9
	walkOpen        = "open"
	walkDrive       = "drive"
	walkShareExe    = `\\server\share\docker.exe`
	walkPipeDocker  = `\\.\pipe\docker`
)

func (s *lookupModelSuite) walkSpy() *lookupMetadataSpy {
	return &lookupMetadataSpy{t: s.T(), target: walkLocalVolume}
}

func (s *lookupModelSuite) TestWindowsWalkRefusesUnsafePathsBeforeAnyMetadata() {
	for _, path := range []string{
		walkShareExe, `\\?\UNC\server\share\docker.exe`,
		walkPipeDocker, `\\?\C:\docker.exe`, `\??\C:\docker.exe`,
		`C:docker.exe`, `\docker.exe`, `C:\safe\..\docker.exe`,
		`C:\safe\docker.exe:stream`, `C:\safe\NUL.exe`, `C:\safe\COM1`, `C:\safe\trailing.`, `C:\safe\trailing `,
		`C:\safe\docker.exe\`, `C:\safe\bin/`, `C:\safe\a*b.exe`, `C:\safe\a?b.exe`,
		"C:\\safe\\a\x00b.exe", "C:\\safe\\a\x1fb.exe", `C:`, "",
		`C:\safe\` + strings.Repeat("a", 256) + `.exe`, `C:\` + strings.Repeat(`a\`, 20000) + `x`,
	} {
		spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}

		observed, err := inspectWindowsLookup(context.Background(), path, spy)

		s.Require().ErrorIs(err, errLookupDeferred, path)
		s.Nil(observed.info, path)
		s.Empty(spy.calls, path)
	}
}

func (s *lookupModelSuite) TestWindowsWalkRefusesMappedAndNonDiskVolumesBeforeOpening() {
	for _, target := range []string{
		`\Device\Mup\server\share`, `\??\UNC\server\share`, `\??\C:\alias`,
		`\Device\HarddiskVolume1\alias`, `\Device\CdRom0`, `\Device\HarddiskVolume`, "",
	} {
		spy := &lookupMetadataSpy{t: s.T(), target: target}

		_, err := inspectWindowsLookup(context.Background(), `Z:\bin\docker.exe`, spy)

		s.Require().ErrorIs(err, errLookupDeferred, target)
		s.Equal([]string{"drive Z:"}, spy.calls, target)
	}
}

func (s *lookupModelSuite) TestWindowsWalkStopsAtAReparsePointWithoutOpeningBelowIt() {
	for _, at := range []uintptr{1, 2, 3, 4} {
		spy := s.walkSpy()
		spy.reparseAt = at

		observed, err := inspectWindowsLookup(context.Background(), `C:\safe\link\docker.exe`, spy)

		s.Require().ErrorIs(err, errLookupDeferred, at)
		s.True(observed.symlink, at)
		s.Nil(observed.info, at)
		s.Empty(observed.resolved, at)
		s.Equal(at, spy.opens, at)
		s.Empty(spy.live, at)
	}
}

func (s *lookupModelSuite) TestWindowsWalkIdentifiesTheFileFromTheLastHandle() {
	spy := s.walkSpy()

	observed, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().NoError(err)
	s.Same(spy.lastInfo, observed.info)
	s.Equal(walkSafeFile, observed.resolved)
	s.False(observed.symlink)
	s.Equal(uintptr(4), spy.opens)
	s.Empty(spy.live)
	s.Equal([]string{
		"drive C:", `open \Device\HarddiskVolume7\`, walkStat, "open safe", walkStat,
		"open bin", walkStat, "open docker.exe", walkStat,
	}, spy.calls)
}

func (s *lookupModelSuite) TestWindowsWalkClosesEveryHandleOnFailure() {
	for _, failure := range []string{walkOpen, walkStat} {
		for failAt := 1; failAt <= 4; failAt++ {
			name := fmt.Sprintf("%s %d", failure, failAt)
			spy := s.walkSpy()
			spy.failure, spy.failAt = failure, failAt

			_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

			s.Require().Error(err, name)
			s.Empty(spy.live, name)
		}
	}
	spy := s.walkSpy()
	spy.fileAt = 2

	_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().Error(err)
	s.Empty(spy.live)
}

func (s *lookupModelSuite) TestWindowsWalkDefersWhenTheDriveCannotBeResolved() {
	spy := s.walkSpy()
	spy.failure = walkDrive

	_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().ErrorIs(err, errLookupDeferred)
	s.Equal([]string{"drive C:"}, spy.calls)
}

func (s *lookupModelSuite) TestWindowsWalkRequiresADirectoryForEveryEntryWithChildren() {
	spy := s.walkSpy()

	_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().NoError(err)
	s.Equal([]bool{true, true, true, false}, spy.directories)
}

func (s *lookupModelSuite) TestWindowsWalkOpensEachChildFromThePreviousHandle() {
	spy := s.walkSpy()

	_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().NoError(err)
	s.Equal([]uintptr{0, 1, 2, 3}, spy.parents)
}

func (s *lookupModelSuite) TestWindowsWalkStopsWhenCanceledWhileClosingAParent() {
	for closeAt := 1; closeAt <= 3; closeAt++ {
		ctx, cancel := context.WithCancel(context.Background())
		spy := s.walkSpy()
		closes := 0
		spy.onClose = func() {
			closes++
			if closes == closeAt {
				cancel()
			}
		}

		_, err := inspectWindowsLookup(ctx, walkSafeFile, spy)
		cancel()

		s.Require().ErrorIs(err, context.Canceled, closeAt)
		s.Empty(spy.live, closeAt)
		s.Len(spy.parents, closeAt+1, closeAt)
	}
}

func (s *lookupModelSuite) TestWindowsWalkStepsRefuseACanceledContextBeforeCalling() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}

	_, inspectErr := inspectWindowsLookup(ctx, walkSafeFile, spy)
	_, openErr := openWindowsLookup(ctx, windowsLookupOpen{name: "x"}, spy)
	_, _, statErr := statWindowsLookup(ctx, 1, spy)

	s.Require().ErrorIs(inspectErr, context.Canceled)
	s.Require().ErrorIs(openErr, context.Canceled)
	s.Require().ErrorIs(statErr, context.Canceled)
	s.Empty(spy.calls)
}

func (s *lookupModelSuite) TestWindowsWalkRefusesShortNamesBeforeAnyMetadata() {
	for _, path := range []string{
		`C:\PROGRA~1\Volta\volta.exe`,
		`C:\Users\x\APPDAT~1\Local\Volta\bin\node.exe`,
		`C:\safe\a~1b\docker.exe`,
		`C:\safe\docker~2.exe`,
		`C:\safe\~0`,
		`C:/PROGRA~1/Volta/volta.exe`,
	} {
		spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}

		_, err := inspectWindowsLookup(context.Background(), path, spy)

		s.Require().ErrorIs(err, errLookupDeferred, path)
		s.Empty(spy.calls, path)
	}
}

func (s *lookupModelSuite) TestWindowsWalkKeepsNamesWithATildeThatIsNotAShortName() {
	for _, part := range []string{"a~b", "~", "tilde~", "~x1", "docker~.exe"} {
		s.True(windowsLookupComponent(part), part)
	}
	for _, part := range []string{"PROGRA~1", "a~1b", "x~9", "~0"} {
		s.False(windowsLookupComponent(part), part)
	}
}

func (s *lookupModelSuite) TestWindowsWalkNormalizesTheResolvedSpelling() {
	spy := s.walkSpy()

	observed, err := inspectWindowsLookup(context.Background(), `C:/safe//bin\docker.exe`, spy)

	s.Require().NoError(err)
	s.Equal(walkSafeFile, observed.resolved)
}

func (s *lookupModelSuite) TestWindowsWalkTreatsAFileInTheMiddleAsMissing() {
	spy := s.walkSpy()
	spy.fileAt = 2

	_, err := inspectWindowsLookup(context.Background(), walkSafeFile, spy)

	s.Require().ErrorIs(err, fs.ErrNotExist)
	s.Equal(uintptr(2), spy.opens)
}

func (s *lookupModelSuite) TestWindowsWalkStopsAtEveryMetadataBoundaryOnCancellation() {
	for cancelAt := 1; cancelAt <= walkSteps; cancelAt++ {
		ctx, cancel := context.WithCancel(context.Background())
		spy := s.walkSpy()
		spy.afterCall = func() {
			if len(spy.calls) == cancelAt {
				cancel()
			}
		}

		observed, err := inspectWindowsLookup(ctx, walkSafeFile, spy)
		cancel()

		s.Require().ErrorIs(err, context.Canceled, cancelAt)
		s.Equal(lookupFile{}, observed, cancelAt)
		s.Len(spy.calls, cancelAt)
		s.Empty(spy.live, cancelAt)
	}
}

func (s *lookupModelSuite) TestWindowsWalkComponentAndVolumePolicy() {
	for _, part := range []string{
		".", "..", "COM1.exe", "LPT9", "con", "trailing ", "trailing.", "bad\x00name", "a:b", "a*b",
	} {
		s.False(windowsLookupComponent(part), part)
	}
	for _, part := range []string{" spaced dir", "docker tool.exe", "snow-\u96ea.exe", "COM10.exe"} {
		s.True(windowsLookupComponent(part), part)
	}
	s.True(localWindowsVolume(`\Device\HarddiskVolume123`))
	s.False(localWindowsVolume(strings.Repeat("x", 30)))
	s.False(localWindowsVolume(`\Device\HarddiskVolume1a`))
}

func (s *lookupModelSuite) TestWindowsLookupNeverReachesMetadataForUnsafeEntries() {
	spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}
	environment := s.windowsEnvironment(map[string]string{envPath: strings.Join([]string{
		`\\server\share`, `\\.\pipe`, `\\?\UNC\server\share`, `C:bin`, `\bin`,
		`C:\safe\NUL`, `C:\safe\docker.exe:stream`,
	}, ";")})
	environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		return inspectWindowsLookup(ctx, path, spy)
	}

	result, err := environment.Lookup(context.Background(), LookupRequest{Command: testDocker})

	s.Require().NoError(err)
	s.Empty(result.Candidates)
	s.Equal(LookupMissing, result.ReasonCode)
	s.Empty(spy.calls)
}

func (s *lookupModelSuite) TestWindowsLookupDefersAVerbatimLocationBeforeMetadata() {
	spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		return inspectWindowsLookup(ctx, path, spy)
	}

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{`\\?\C:\tools\docker.exe`},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.Nil(result.Candidates[0].Identity)
	s.Equal(LookupInspectionDeferred, result.ReasonCode)
	s.Empty(spy.calls)
}

func (s *lookupModelSuite) TestWindowsLookupRefusesRemoteAndDeviceRequestsBeforeMetadata() {
	spy := &lookupMetadataSpy{t: s.T(), forbidCalls: true}
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		return inspectWindowsLookup(ctx, path, spy)
	}
	for _, command := range []string{
		walkShareExe, `\\.\pipe\docker.exe`, driveRelativeExe, "CON.exe", `\a.exe`,
	} {
		result, err := environment.Lookup(context.Background(), LookupRequest{
			Command: command, Explicit: true,
		})

		s.Require().NoError(err, command)
		s.Empty(result.Candidates, command)
		s.Equal(LookupInvalidPath, result.ReasonCode, command)
	}
	_, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{walkShareExe},
	})
	s.Require().Error(err)
	s.Empty(spy.calls)
}

func (s *lookupModelSuite) TestWindowsLookupRecordsTheHandleInfoAsTheIdentity() {
	spy := s.walkSpy()
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		return inspectWindowsLookup(ctx, path, spy)
	}

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{walkSafeFile},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileExecutable, result.Candidates[0].State)
	s.Empty(result.ReasonCode)
	s.Equal(walkSafeFile, result.Candidates[0].ResolvedPath)
	s.Require().NotNil(result.Candidates[0].Identity)
	s.Same(spy.lastInfo, result.Candidates[0].Identity.info)
}

func (s *lookupModelSuite) TestWindowsLookupReportsAReparsePointAsADeferredLink() {
	spy := s.walkSpy()
	spy.reparseAt = 2
	environment := s.windowsEnvironment(nil)
	environment.inspect = func(ctx context.Context, path string) (lookupFile, error) {
		return inspectWindowsLookup(ctx, path, spy)
	}

	result, err := environment.Lookup(context.Background(), LookupRequest{
		Command: testDocker, ReviewedLocations: []string{`C:\safe\link\docker.exe`},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Candidates, 1)
	s.Equal(FileDeferred, result.Candidates[0].State)
	s.True(result.Candidates[0].IsSymlink)
	s.Empty(result.Candidates[0].ResolvedPath)
	s.Nil(result.Candidates[0].Identity)
}

type lookupMetadataSpy struct {
	t           testingT
	forbidCalls bool
	target      string
	calls       []string
	opens       uintptr
	reparseAt   uintptr
	fileAt      uintptr
	live        map[uintptr]bool
	failure     string
	failAt      int
	directories []bool
	parents     []uintptr
	onClose     func()
	afterCall   func()
	lastInfo    fs.FileInfo
}

func (m *lookupMetadataSpy) record(call string) {
	m.t.Helper()
	if m.forbidCalls {
		m.t.Fatalf("forbidden metadata call: %s", call)
	}
	m.calls = append(m.calls, call)
	if m.afterCall != nil {
		m.afterCall()
	}
}

func (m *lookupMetadataSpy) driveTarget(drive string) (string, error) {
	m.record("drive " + drive)
	if m.failure == walkDrive {
		return m.target, errors.New("synthetic drive failure")
	}
	return m.target, nil
}

func (m *lookupMetadataSpy) open(request windowsLookupOpen) (uintptr, error) {
	m.record("open " + request.name)
	m.directories = append(m.directories, request.directory)
	m.parents = append(m.parents, request.parent)
	if request.parent != 0 {
		m.expect(m.live[request.parent], "open below a closed handle")
		m.expect(m.reparseAt != request.parent, "open below a reparse point")
		m.expect(!strings.Contains(request.name, `\`), "open of more than one component")
	}
	if m.failure == walkOpen && m.failAt <= 1 {
		return 0, errors.New("synthetic open failure")
	}
	if m.failure == walkOpen {
		m.failAt--
	}
	if m.live == nil {
		m.live = make(map[uintptr]bool)
	}
	m.opens++
	m.live[m.opens] = true
	return m.opens, nil
}

func (m *lookupMetadataSpy) stat(handle uintptr) (fs.FileInfo, bool, error) {
	m.record(walkStat)
	m.expect(m.live[handle], "stat of a closed handle")
	if m.failure == walkStat && m.failAt <= 1 {
		return nil, false, errors.New("synthetic stat failure")
	}
	if m.failure == walkStat {
		m.failAt--
	}
	m.lastInfo = &walkInfo{directory: handle < 4 && handle != m.fileAt}
	return m.lastInfo, handle == m.reparseAt, nil
}

func (m *lookupMetadataSpy) close(handle uintptr) {
	m.expect(m.live[handle], "close of a handle that is not open")
	delete(m.live, handle)
	if m.onClose != nil {
		m.onClose()
	}
}

func (m *lookupMetadataSpy) expect(ok bool, message string) {
	m.t.Helper()
	if !ok {
		m.t.Errorf("%s", message)
	}
}

type testingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

type walkInfo struct{ directory bool }

func (i *walkInfo) Name() string { return "entry" }
func (i *walkInfo) Size() int64  { return 0 }
func (i *walkInfo) Mode() fs.FileMode {
	if i.directory {
		return fs.ModeDir | 0o700
	}
	return 0o700
}
func (i *walkInfo) ModTime() time.Time { return time.Time{} }
func (i *walkInfo) IsDir() bool        { return i.directory }
func (i *walkInfo) Sys() any           { return nil }

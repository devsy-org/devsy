//go:build !windows

package audit

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/stretchr/testify/suite"
)

const (
	readerFutureDay = "2026-11-24"
	readerSession   = "keep"
	readerFragment  = `{"torn":`
)

type ReaderSuite struct {
	suite.Suite
	dir string
	now time.Time
}

func TestReaderSuite(t *testing.T) {
	suite.Run(t, new(ReaderSuite))
}

func (s *ReaderSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.now = time.Date(2026, time.November, 22, 20, 0, 0, 0, time.FixedZone("west", -8*60*60))
}

func (s *ReaderSuite) TestDayAndFileOrder() {
	future := s.appendEvent(readerFutureDay, "future", s.now.AddDate(0, 0, 1))
	first := s.appendEvent(dayOne, "first", s.now.AddDate(0, 0, -1))
	today := s.appendEvent(dayTwo, "today", s.now)
	second := s.appendEvent(dayOne, "second", s.now.AddDate(0, 0, -1))
	s.Require().
		NoError(os.WriteFile(filepath.Join(s.dir, "notes.jsonl"), []byte("ignore"), fileMode))
	got, err := s.scan(ReaderFilter{})
	s.Require().NoError(err)
	s.Equal([]string{first, second, today, future}, got)
}

func (s *ReaderSuite) TestSessionFilter() {
	first := s.appendEvent(dayTwo, readerSession, s.now)
	s.appendEvent(dayTwo, "ignore", s.now)
	last := s.appendEvent(readerFutureDay, readerSession, s.now.AddDate(0, 0, 1))
	got, err := s.scan(ReaderFilter{Session: readerSession})
	s.Require().NoError(err)
	s.Equal([]string{first, last}, got)
}

func (s *ReaderSuite) TestSincePrunesDaysAndKeepsBoundary() {
	s.writeDay(dayOne, "corrupt\n")
	s.appendEvent(dayTwo, readerSession, s.now.Add(-time.Second))
	s.appendEvent(dayTwo, "ignore-session", s.now)
	want := s.appendEvent(dayTwo, readerSession, s.now)
	got, err := s.scan(ReaderFilter{Session: readerSession, Since: &s.now})
	s.Require().NoError(err)
	s.Equal([]string{want}, got)
}

func (s *ReaderSuite) TestInvalidTimestampFailsEvenForNonmatchingSession() {
	first := s.appendEvent(dayTwo, readerSession, s.now)
	bad, err := json.Marshal(envelope.Envelope{Session: "ignore", TS: "invalid"})
	s.Require().NoError(err)
	s.writeDay(dayTwo, first+"\n"+string(bad)+"\n")
	got, err := s.scan(ReaderFilter{Session: readerSession, Since: &s.now})
	s.Require().ErrorContains(err, "timestamp must be RFC3339")
	s.checkLocation(err, dayTwo, len(first)+1)
	s.Equal([]string{first}, got)
}

func (s *ReaderSuite) TestOpaqueRecordIsNotDecodedWithoutFilters() {
	const record = `{"v": "not an envelope", "number": 1e999 }`
	s.writeDay(dayTwo, record+"\n")
	got, err := s.scan(ReaderFilter{})
	s.Require().NoError(err)
	s.Equal([]string{record}, got)
}

func (s *ReaderSuite) TestNewestDayTornTailIsSilent() {
	s.writeDay(dayOne, "{\"n\":1}\n")
	s.writeDay(readerFutureDay, "{}\n"+readerFragment)
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, "z.txt"), nil, fileMode))
	got, warnings, err := s.scanWithWarnings(ReaderFilter{})
	s.Require().NoError(err)
	s.Empty(warnings)
	s.Equal([]string{`{"n":1}`, "{}"}, got)
}

func (s *ReaderSuite) TestOlderDayTornTailWarnsAndContinues() {
	s.writeDay(dayOne, "{}\n"+readerFragment)
	s.writeDay(dayTwo, "{\"n\":2}\n"+readerFragment)
	s.writeDay(readerFutureDay, "{\"n\":3}\n"+readerFragment)
	got, warnings, err := s.scanWithWarnings(ReaderFilter{})
	s.Require().NoError(err)
	s.Require().Len(warnings, 2)
	s.checkLocation(warnings[0], dayOne, len("{}\n"))
	s.checkLocation(warnings[1], dayTwo, len("{\"n\":2}\n"))
	s.Equal([]string{"{}", `{"n":2}`, `{"n":3}`}, got)
}

func (s *ReaderSuite) TestPrunedDaysDoNotWarn() {
	s.writeDay(dayOne, readerFragment)
	want := s.appendEvent(dayTwo, readerSession, s.now)
	s.writeDay(dayTwo, want+"\n"+readerFragment)
	got, warnings, err := s.scanWithWarnings(ReaderFilter{Since: &s.now})
	s.Require().NoError(err)
	s.Empty(warnings)
	s.Equal([]string{want}, got)

	later := s.now.AddDate(0, 0, 1)
	got, warnings, err = s.scanWithWarnings(ReaderFilter{Since: &later})
	s.Require().NoError(err)
	s.Empty(warnings)
	s.Empty(got)
}

func (s *ReaderSuite) TestNewestDayMalformedLineStopsAtOffset() {
	for _, bad := range []string{"", "[]", `{"cut":`, "{\r}", " {}"} {
		s.Run(fmt.Sprintf("%q", bad), func() {
			const prefix = "{ \"x\": 1 }\n{}\n"
			s.writeDay(dayTwo, prefix+bad+"\n{}\n")
			got, warnings, err := s.scanWithWarnings(ReaderFilter{})
			s.Require().Error(err)
			s.Empty(warnings)
			s.checkLocation(err, dayTwo, len(prefix))
			s.Equal([]string{`{ "x": 1 }`, "{}"}, got)
		})
	}
}

func (s *ReaderSuite) TestOlderDayMalformedLineStopsAtOffset() {
	s.writeDay(dayOne, "{}\n[]\n")
	s.writeDay(dayTwo, "{\"n\":2}\n")
	got, warnings, err := s.scanWithWarnings(ReaderFilter{})
	s.Require().ErrorIs(err, errLineShape)
	s.Empty(warnings)
	s.checkLocation(err, dayOne, len("{}\n"))
	s.Equal([]string{"{}"}, got)
}

func (s *ReaderSuite) TestLineLimitWithLargeFile() {
	const count = 128
	atLimit := "{" + strings.Repeat(" ", MaxLineBytes-2) + "}"
	for _, over := range []string{atLimit + " ", strings.Repeat(atLimit, 3)} {
		s.writeDay(dayTwo, strings.Repeat(atLimit+"\n", count)+over+"\n{}\n")
		delivered := 0
		err := s.reader().Scan(ReaderFilter{}, func(record []byte) error {
			s.Len(record, MaxLineBytes)
			delivered++
			return nil
		}, nil)
		s.Require().ErrorIs(err, errLineTooLong)
		s.checkLocation(err, dayTwo, count*(MaxLineBytes+1))
		s.Equal(count, delivered)
	}
}

func (s *ReaderSuite) TestOversizedTornTailIsDiscardedInChunks() {
	const prefix = "{}\n"
	for _, size := range []int{MaxLineBytes + 1, 2 * (MaxLineBytes + 2), 3 * MaxLineBytes} {
		fragment := strings.Repeat("x", size)
		s.writeDay(dayOne, prefix+fragment)
		s.writeDay(dayTwo, prefix+fragment)
		got, warnings, err := s.scanWithWarnings(ReaderFilter{})
		s.Require().NoError(err)
		s.Require().Len(warnings, 1)
		s.checkLocation(warnings[0], dayOne, len(prefix))
		s.Equal([]string{"{}", "{}"}, got)
	}
}

func (s *ReaderSuite) TestReadsWhileWriterHoldsLock() {
	want := s.appendEvent(dayTwo, "active", s.now)
	lock, err := newFlock(filepath.Join(s.dir, lockName))
	s.Require().NoError(err)
	s.Require().NoError(lock.Lock())
	defer func() { _ = lock.Close() }()
	reader := s.reader()
	done := make(chan error, 1)
	var got string
	go func() {
		done <- reader.Scan(ReaderFilter{}, func(line []byte) error {
			got = string(line)
			return nil
		}, nil)
	}()
	select {
	case err := <-done:
		s.Require().NoError(err)
		s.Equal(want, got)
	case <-time.After(time.Second):
		s.FailNow("reader waited for writer lock")
	}
}

func (s *ReaderSuite) TestLeavesDirectoryAndFilesUnchanged() {
	const stored = "{ \"a\": 1 }\n{\"torn\":"
	path := s.writeDay(dayTwo, stored)
	then := s.now.Add(-time.Hour)
	s.Require().NoError(os.Chtimes(path, then, then))
	s.Require().NoError(os.Chtimes(s.dir, then, then))
	before := s.modTime(path)
	dirBefore := s.modTime(s.dir)
	syncs := recordSyncs(s.T())
	got, err := s.scan(ReaderFilter{})
	s.Require().NoError(err)
	s.Equal([]string{`{ "a": 1 }`}, got)
	s.Equal(stored, s.readFile(path))
	s.Equal(before, s.modTime(path))
	s.Equal(dirBefore, s.modTime(s.dir))
	s.Empty(*syncs)
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
	s.Equal(dayTwo+fileExt, entries[0].Name())
}

func (s *ReaderSuite) TestMissingDirectoryIsNotCreated() {
	missing := filepath.Join(s.dir, "missing")
	_, err := OpenReader(missing)
	s.Require().ErrorIs(err, fs.ErrNotExist)
	s.NoDirExists(missing)
}

func (s *ReaderSuite) TestRegularFileIsNotAnAuditDirectory() {
	path := s.writeDay(dayTwo, "{}\n")
	_, err := OpenReader(path)
	s.ErrorContains(err, "not a directory")
}

func (s *ReaderSuite) TestSymlinkDayIsRefused() {
	target := filepath.Join(s.T().TempDir(), "target")
	s.Require().NoError(os.WriteFile(target, []byte("{}\n"), fileMode))
	s.Require().NoError(os.Symlink(target, filepath.Join(s.dir, dayTwo+fileExt)))
	got, err := s.scan(ReaderFilter{})
	s.Require().ErrorContains(err, "not a regular file")
	s.ErrorContains(err, dayTwo+fileExt)
	s.Empty(got)
}

func (s *ReaderSuite) TestWritableDirectoryIsRefused() {
	for _, access := range []os.FileMode{0o020, 0o002} {
		s.Require().NoError(os.Chmod(s.dir, dirMode|access))
		_, err := OpenReader(s.dir)
		s.ErrorContains(err, "must not be writable")
	}
}

func (s *ReaderSuite) TestForeignDirectoryIsRefused() {
	if os.Geteuid() == 0 {
		s.T().Skip("test requires a non-root user")
	}
	_, err := OpenReader(string(filepath.Separator))
	s.ErrorContains(err, "must be owned by the current user")
}

func (s *ReaderSuite) reader() *Reader {
	r, err := OpenReader(s.dir)
	s.Require().NoError(err)
	return r
}

func (s *ReaderSuite) scan(filter ReaderFilter) ([]string, error) {
	got, _, err := s.scanWithWarnings(filter)
	return got, err
}

func (s *ReaderSuite) scanWithWarnings(filter ReaderFilter) ([]string, []error, error) {
	var got []string
	var warnings []error
	err := s.reader().Scan(filter, func(line []byte) error {
		got = append(got, string(line))
		return nil
	}, func(warning error) {
		warnings = append(warnings, warning)
	})
	return got, warnings, err
}

func (s *ReaderSuite) appendEvent(day, session string, ts time.Time) string {
	st, err := Open(s.dir)
	s.Require().NoError(err)
	dayTime, err := time.Parse(dayLayout, day)
	s.Require().NoError(err)
	st.now = func() time.Time { return dayTime }
	writer, err := NewWriter(st, WriterConfig{
		Pool:          "test-pool",
		Host:          "test-host",
		PolicyVersion: "test-policy",
		ConfigVersion: "test-config",
		Validate:      acceptAuditDataForTest,
		Now:           func() time.Time { return ts },
	})
	s.Require().NoError(err)
	event := writerTestEvent(envelope.AuditSessionRequest)
	event.Session = session
	s.Require().NoError(writer.Write(event))
	content := strings.TrimSuffix(s.readFile(filepath.Join(s.dir, day+fileExt)), "\n")
	return content[strings.LastIndexByte(content, '\n')+1:]
}

func (s *ReaderSuite) checkLocation(err error, day string, offset int) {
	s.ErrorContains(
		err,
		fmt.Sprintf("%s at byte offset %d:", filepath.Join(s.dir, day+fileExt), offset),
	)
}

func (s *ReaderSuite) writeDay(day, content string) string {
	path := filepath.Join(s.dir, day+fileExt)
	s.Require().NoError(os.WriteFile(path, []byte(content), fileMode))
	return path
}

func (s *ReaderSuite) readFile(path string) string {
	b, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)
	return string(b)
}

func (s *ReaderSuite) modTime(path string) time.Time {
	info, err := os.Stat(path)
	s.Require().NoError(err)
	return info.ModTime()
}

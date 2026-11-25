package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	dayOne = "2026-11-22"
	dayTwo = "2026-11-23"
)

type StoreSuite struct {
	suite.Suite
	dir   string
	clock time.Time
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (s *StoreSuite) SetupTest() {
	s.dir = filepath.Join(s.T().TempDir(), "audit")
	s.clock = time.Date(2026, 11, 22, 23, 59, 0, 0, time.UTC)
}

func (s *StoreSuite) TestAppendWritesOneLinePerRecord() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"a":1}`)))
	s.Require().NoError(st.Append([]byte(`{"b":"x y"}`)))
	s.Equal("{\"a\":1}\n{\"b\":\"x y\"}\n", s.read(dayOne))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(s.dir, dayOne+fileExt))
		s.Require().NoError(err)
		s.Equal(os.FileMode(fileMode), info.Mode().Perm())
	}
}

func (s *StoreSuite) TestRotatesAtUTCMidnight() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	s.clock = s.clock.Add(2 * time.Minute)
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Equal("{\"n\":1}\n", s.read(dayOne))
	s.Equal("{\"n\":2}\n", s.read(dayTwo))
	days, err := st.Days()
	s.Require().NoError(err)
	s.Equal([]string{dayOne, dayTwo}, days)
}

func (s *StoreSuite) TestRefusesBadRecords() {
	st := s.open()
	cases := map[string][]byte{
		"empty":     nil,
		"newline":   []byte("{\"a\":1}\n{\"b\":2}"),
		"cr":        []byte("{\"a\":\r1}"),
		"array":     []byte("[1]"),
		"scalar":    []byte("1"),
		"truncated": []byte(`{"a":`),
		"too long":  []byte(`{"a":"` + strings.Repeat("x", MaxLineBytes) + `"}`),
	}
	for name, record := range cases {
		s.Error(st.Append(record), name)
	}
	_, err := os.Stat(filepath.Join(s.dir, dayOne+fileExt))
	s.True(os.IsNotExist(err), "a refused record must not create a file")
}

func (s *StoreSuite) TestWriteFailureIsAnError() {
	st := s.open()
	s.Require().NoError(os.Mkdir(filepath.Join(s.dir, dayOne+fileExt), dirMode))
	s.Error(st.Append([]byte(`{"a":1}`)))
}

func (s *StoreSuite) TestConcurrentWritersDoNotInterleave() {
	const writers, perWriter = 8, 40
	pad := strings.Repeat("p", 4096)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			st := s.open()
			for i := range perWriter {
				rec := fmt.Sprintf(`{"w":%d,"i":%d,"pad":"%s"}`, w, i, pad)
				s.NoError(st.Append([]byte(rec)))
			}
		})
	}
	wg.Wait()
	seen := map[[2]int]bool{}
	for line := range strings.SplitSeq(strings.TrimSuffix(s.read(dayOne), "\n"), "\n") {
		var rec struct{ W, I int }
		s.Require().NoError(json.Unmarshal([]byte(line), &rec), "interleaved line")
		seen[[2]int{rec.W, rec.I}] = true
	}
	s.Len(seen, writers*perWriter)
}

func (s *StoreSuite) TestScanOrderAndBounds() {
	st := s.open()
	s.Require().NoError(st.Append([]byte(`{"n":1}`)))
	s.clock = s.clock.Add(time.Minute)
	s.Require().NoError(st.Append([]byte(`{"n":2}`)))
	s.Require().NoError(st.Append([]byte(`{"n":3}`)))
	collect := func(from, to string) []string {
		var out []string
		s.Require().NoError(st.Scan(from, to, func(day string, rec []byte) error {
			out = append(out, day+" "+string(rec))
			return nil
		}))
		return out
	}
	s.Equal(
		[]string{dayOne + " {\"n\":1}", dayTwo + " {\"n\":2}", dayTwo + " {\"n\":3}"},
		collect("", ""),
	)
	s.Equal([]string{dayTwo + " {\"n\":2}", dayTwo + " {\"n\":3}"}, collect(dayTwo, ""))
	s.Equal([]string{dayOne + " {\"n\":1}"}, collect("", dayOne))
	stop := fmt.Errorf("stop")
	s.Equal(stop, st.Scan("", "", func(string, []byte) error { return stop }))
}

func (s *StoreSuite) TestScanReportsPartialLine() {
	st := s.open()
	s.Require().NoError(os.WriteFile(
		filepath.Join(s.dir, dayOne+fileExt), []byte("{\"a\":1}\n{\"b\""), fileMode))
	var got []string
	err := st.Scan("", "", func(_ string, rec []byte) error {
		got = append(got, string(rec))
		return nil
	})
	s.ErrorIs(err, io.ErrUnexpectedEOF)
	s.Equal([]string{"{\"a\":1}"}, got)
}

func (s *StoreSuite) TestDaysIgnoresOtherFiles() {
	st := s.open()
	for _, name := range []string{".lock", "notes.txt", "2026-11-22.jsonl.bak", "2026-11-22.jsonl"} {
		s.Require().NoError(os.WriteFile(filepath.Join(s.dir, name), nil, fileMode))
	}
	days, err := st.Days()
	s.Require().NoError(err)
	s.Equal([]string{dayOne}, days)
}

func (s *StoreSuite) TestOpenRefusesWritableDirectory() {
	if runtime.GOOS == "windows" {
		s.T().Skip("no unix permission bits")
	}
	s.Require().NoError(os.MkdirAll(s.dir, dirMode))
	s.Require().NoError(os.Chmod(s.dir, dirMode|0o070))
	_, err := Open(s.dir)
	s.ErrorContains(err, "must not be writable")
}

func (s *StoreSuite) TestHostIDIsMintedOnceAndKept() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	first, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Regexp(hostIDPattern, first)
	again, err := LoadOrCreateHostID(path)
	s.Require().NoError(err)
	s.Equal(first, again)
}

func (s *StoreSuite) TestHostIDNeverReplaced() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	s.Require().NoError(os.WriteFile(path, []byte("CANARY"), fileMode))
	_, err := LoadOrCreateHostID(path)
	s.Require().Error(err)
	s.NotContains(err.Error(), "CANARY")
	data, err := os.ReadFile(filepath.Clean(path))
	s.Require().NoError(err)
	s.True(bytes.Equal([]byte("CANARY"), data))
}

func (s *StoreSuite) TestHostIDConcurrentFirstUseAgrees() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			id, err := LoadOrCreateHostID(path)
			s.NoError(err)
			ids[i] = id
		})
	}
	wg.Wait()
	for _, id := range ids {
		s.Equal(ids[0], id)
	}
}

func (s *StoreSuite) TestNewIDIsULID() {
	a, b := NewID(), NewID()
	s.Len(a, 26)
	s.NotEqual(a, b)
}

func (s *StoreSuite) open() *Store {
	st, err := Open(s.dir)
	s.Require().NoError(err)
	st.now = func() time.Time { return s.clock }
	return st
}

func (s *StoreSuite) read(day string) string {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(s.dir, day+fileExt)))
	s.Require().NoError(err)
	return string(data)
}

// Package audit stores pool audit records as one JSON object per line in
// daily files. It treats each record as opaque bytes: the record shape and
// type names belong to the shared audit contract, not to this package.
package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gofrs/flock"
)

const (
	// MaxLineBytes bounds one record. A larger line is refused, never cut.
	MaxLineBytes = 64 * 1024

	dayLayout = "2006-01-02"
	fileExt   = ".jsonl"
	lockName  = ".lock"
	dirMode   = 0o700
	fileMode  = 0o600
)

var (
	dayFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)

	errLineEmpty   = errors.New("audit record is empty")
	errLineTooLong = fmt.Errorf("audit record is longer than %d bytes", MaxLineBytes)
	errLineShape   = errors.New("audit record must be one single-line JSON object")
	errTornTail    = errors.New("audit file ends in an unterminated record longer than one record")
)

// Store appends records to <dir>/<YYYY-MM-DD>.jsonl, one file per UTC day.
type Store struct {
	dir string
	now func() time.Time
}

// Open creates the directory when needed and refuses one that other users
// can write to.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	if err := checkDirMode(dir); err != nil {
		return nil, err
	}
	return &Store{dir: dir, now: time.Now}, nil
}

// Append writes one record and syncs it before returning. Any error means
// the record is not stored, so the caller must refuse the action it covers.
func (s *Store) Append(record []byte) error {
	if err := checkRecord(record); err != nil {
		return err
	}
	lockPath := filepath.Join(s.dir, lockName)
	if err := checkRegular(lockPath); err != nil {
		return err
	}
	lock := flock.New(lockPath, flock.SetPermissions(fileMode))
	defer func() { _ = lock.Close() }()
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("lock audit directory: %w", err)
	}
	return appendLine(filepath.Join(s.dir, s.now().UTC().Format(dayLayout)+fileExt), record)
}

func checkRecord(record []byte) error {
	switch {
	case len(record) == 0:
		return errLineEmpty
	case len(record) > MaxLineBytes:
		return errLineTooLong
	case record[0] != '{' || bytes.ContainsAny(record, "\r\n") || !json.Valid(record):
		return errLineShape
	}
	return nil
}

// appendLine writes the record and its newline in one call. A torn tail from
// an earlier crash is cut off first, and a failed or short write is cut back,
// so a record never joins a partial line. A sync error is returned after the
// line is written, so the record may exist although the caller must refuse.
func appendLine(path string, record []byte) (err error) {
	if err := checkRegular(path); err != nil {
		return err
	}
	f, err := os.OpenFile(
		filepath.Clean(path),
		os.O_RDWR|os.O_APPEND|os.O_CREATE|noFollow,
		fileMode,
	)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close audit file: %w", cerr)
		}
	}()
	size, err := trimTornTail(f)
	if err != nil {
		return err
	}
	line := append(append(make([]byte, 0, len(record)+1), record...), '\n')
	if _, err := f.Write(line); err != nil {
		_ = f.Truncate(size)
		return fmt.Errorf("write audit record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync audit file: %w", err)
	}
	return nil
}

// trimTornTail removes bytes after the last newline, a record cut short by a
// crash, and returns the size that remains.
func trimTornTail(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat audit file: %w", err)
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}
	window := min(size, int64(MaxLineBytes+1))
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil {
		return 0, fmt.Errorf("read audit file tail: %w", err)
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last == len(buf)-1 {
		return size, nil
	}
	if last < 0 && window < size {
		return 0, errTornTail
	}
	keep := size - window + int64(last+1)
	if err := f.Truncate(keep); err != nil {
		return 0, fmt.Errorf("cut torn audit record: %w", err)
	}
	return keep, nil
}

// checkRegular refuses a path that exists as anything but a regular file,
// such as a symlink planted in the audit directory.
func checkRegular(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat audit file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("audit path is not a regular file: %s", path)
	}
	return nil
}

// Days lists the days that have a file, oldest first.
func (s *Store) Days() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read audit directory: %w", err)
	}
	var days []string
	for _, e := range entries {
		if e.Type().IsRegular() && dayFilePattern.MatchString(e.Name()) {
			days = append(days, e.Name()[:len(dayLayout)])
		}
	}
	return days, nil
}

// Scan calls fn for every record of each day in [from, to], oldest first. An
// empty bound is open. It stops at the first error from fn. A line cut short
// by a crash is reported as io.ErrUnexpectedEOF.
func (s *Store) Scan(from, to string, fn func(day string, record []byte) error) error {
	days, err := s.Days()
	if err != nil {
		return err
	}
	for _, day := range days {
		if (from != "" && day < from) || (to != "" && day > to) {
			continue
		}
		if err := s.scanDay(day, fn); err != nil {
			return err
		}
	}
	return nil
}

// scanDay fixes the file size under the shared lock, then releases it before
// calling fn, so a callback never blocks or deadlocks an append. The file only
// grows, so the first size bytes are the snapshot.
func (s *Store) scanDay(day string, fn func(string, []byte) error) error {
	f, size, err := s.openSnapshot(day)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return eachLine(io.LimitReader(f, size), func(line []byte) error { return fn(day, line) })
}

func (s *Store) openSnapshot(day string) (*os.File, int64, error) {
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
	defer func() { _ = lock.Close() }()
	if err := lock.RLock(); err != nil {
		return nil, 0, fmt.Errorf("lock audit directory: %w", err)
	}
	path := filepath.Clean(filepath.Join(s.dir, day+fileExt))
	if err := checkRegular(path); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read audit file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("stat audit file: %w", err)
	}
	return f, info.Size(), nil
}

// eachLine passes each line without its newline. The slice is valid only
// until fn returns.
func eachLine(r io.Reader, fn func([]byte) error) error {
	br := bufio.NewReaderSize(r, MaxLineBytes+2)
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			return endOfLines(line, err)
		}
		if ferr := fn(line[:len(line)-1]); ferr != nil {
			return ferr
		}
	}
}

func endOfLines(rest []byte, err error) error {
	switch {
	case errors.Is(err, io.EOF) && len(rest) == 0:
		return nil
	case errors.Is(err, io.EOF):
		return io.ErrUnexpectedEOF
	case errors.Is(err, bufio.ErrBufferFull):
		return errLineTooLong
	}
	return fmt.Errorf("read audit file: %w", err)
}

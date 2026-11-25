// Package audit stores pool audit records as one JSON object per line in
// daily files. It treats each record as opaque bytes: the record shape and
// type names belong to the shared audit contract, not to this package.
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
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
)

// Store appends records to <dir>/<YYYY-MM-DD>.jsonl, one file per UTC day.
type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
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

// appendLine writes the record and its newline in one call. A failed or
// short write is cut back so a later record never joins a partial line.
func appendLine(path string, record []byte) (err error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_APPEND|os.O_CREATE, fileMode)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close audit file: %w", cerr)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat audit file: %w", err)
	}
	line := append(append(make([]byte, 0, len(record)+1), record...), '\n')
	if _, err := f.Write(line); err != nil {
		_ = f.Truncate(info.Size())
		return fmt.Errorf("write audit record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync audit file: %w", err)
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
// empty bound is open. It reads under a shared lock, so it sees whole lines
// only, and stops at the first error from fn.
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

func (s *Store) scanDay(day string, fn func(string, []byte) error) error {
	lock := flock.New(filepath.Join(s.dir, lockName), flock.SetPermissions(fileMode))
	defer func() { _ = lock.Close() }()
	if err := lock.RLock(); err != nil {
		return fmt.Errorf("lock audit directory: %w", err)
	}
	data, err := os.ReadFile(filepath.Clean(filepath.Join(s.dir, day+fileExt)))
	if err != nil {
		return fmt.Errorf("read audit file: %w", err)
	}
	return eachLine(data, func(line []byte) error { return fn(day, line) })
}

func eachLine(data []byte, fn func([]byte) error) error {
	for len(data) > 0 {
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		if !found {
			return io.ErrUnexpectedEOF
		}
		if err := fn(line); err != nil {
			return err
		}
		data = rest
	}
	return nil
}

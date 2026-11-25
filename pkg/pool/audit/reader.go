package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
)

var errReaderUnsupportedOS = errors.New("reading pool audit files is not supported on Windows")

// Reader reads daily audit files without taking the writer's lock.
type Reader struct {
	dir string
	now func() time.Time
}

// ReaderFilter selects records by session and an optional inclusive time.
type ReaderFilter struct {
	Session string
	Since   *time.Time
}

// OpenReader opens an existing audit directory without changing it.
func OpenReader(dir string) (*Reader, error) {
	if runtime.GOOS == "windows" {
		return nil, errReaderUnsupportedOS
	}
	if err := checkDirMode(dir); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat audit directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("audit path is not a directory: %s", dir)
	}
	return &Reader{dir: dir, now: time.Now}, nil
}

// Scan delivers matching records in day and file order, without their
// newlines. Each slice is valid only until emit returns.
func (r *Reader) Scan(filter ReaderFilter, emit func([]byte) error) error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read audit directory: %w", err)
	}
	today := r.now().UTC().Format(dayLayout)
	from := filter.firstDay()
	for _, entry := range entries {
		name := entry.Name()
		if !dayFilePattern.MatchString(name) || name[:len(dayLayout)] < from {
			continue
		}
		if err := scanReaderDay(
			filepath.Join(r.dir, name),
			name == today+fileExt,
			filter,
			emit,
		); err != nil {
			return err
		}
	}
	return nil
}

func scanReaderDay(path string, current bool, filter ReaderFilter, emit func([]byte) error) error {
	if err := checkRegular(path); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDONLY|noFollow|nonBlock, 0)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := statRegular(f); err != nil {
		return err
	}
	return scanReaderLines(f, current, filter, emit)
}

func scanReaderLines(f *os.File, current bool, filter ReaderFilter, emit func([]byte) error) error {
	lines := bufio.NewReaderSize(f, MaxLineBytes+2)
	var offset int64
	for {
		line, err := lines.ReadSlice('\n')
		if err != nil {
			if err := readerTailError(line, err, current); err != nil {
				return fmt.Errorf("audit file %s at byte offset %d: %w", f.Name(), offset, err)
			}
			return nil
		}
		if err := emitReaderRecord(line[:len(line)-1], filter, emit); err != nil {
			return fmt.Errorf("audit file %s at byte offset %d: %w", f.Name(), offset, err)
		}
		offset += int64(len(line))
	}
}

func readerTailError(line []byte, err error, current bool) error {
	if len(line) > MaxLineBytes {
		return errLineTooLong
	}
	if current && errors.Is(err, io.EOF) {
		return nil
	}
	return endOfLines(line, err)
}

func emitReaderRecord(record []byte, filter ReaderFilter, emit func([]byte) error) error {
	if err := checkRecord(record); err != nil {
		return err
	}
	match, err := filter.matches(record)
	if err != nil || !match {
		return err
	}
	return emit(record)
}

func (f ReaderFilter) firstDay() string {
	if f.Since == nil {
		return ""
	}
	return f.Since.UTC().Format(dayLayout)
}

func (f ReaderFilter) matches(record []byte) (bool, error) {
	if f.Session == "" && f.Since == nil {
		return true, nil
	}
	var e envelope.Envelope
	if err := json.Unmarshal(record, &e); err != nil {
		return false, errors.New("decode audit envelope")
	}
	sessionMatches := f.Session == "" || e.Session == f.Session
	if f.Since == nil {
		return sessionMatches, nil
	}
	ts, err := time.Parse(time.RFC3339, e.TS)
	if err != nil {
		return false, errors.New("audit envelope timestamp must be RFC3339")
	}
	return sessionMatches && !ts.Before(*f.Since), nil
}

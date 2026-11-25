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
	"runtime"
	"slices"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
)

var (
	errReaderUnsupportedOS = errors.New("reading pool audit files is not supported on Windows")
	errReaderTornTail      = errors.New("skipped unterminated audit record")
	errReaderFileChanged   = errors.New("audit file changed while reading")
)

// Reader reads daily audit files without taking the writer's lock.
type Reader struct {
	dir string
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
	return &Reader{dir: dir}, nil
}

// Scan delivers matching records in day and file order, without their
// newlines. Each slice is valid only until emit returns. It skips torn tails,
// calling warn if non-nil for each affected file other than the newest.
func (r *Reader) Scan(filter ReaderFilter, emit func([]byte) error, warn func(error)) error {
	if err := checkDirMode(r.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read audit directory: %w", err)
	}
	newest := newestReaderDay(entries)
	// A clock step between stamping the record and choosing the file can put
	// a matching timestamp in a file named for an earlier day.
	for _, entry := range entries {
		name := entry.Name()
		if !dayFilePattern.MatchString(name) {
			continue
		}
		err := scanReaderDay(filepath.Join(r.dir, name), filter, emit)
		if err := handleReaderDay(err, name == newest, warn); err != nil {
			return err
		}
	}
	return nil
}

func newestReaderDay(entries []os.DirEntry) string {
	for _, entry := range slices.Backward(entries) {
		if name := entry.Name(); dayFilePattern.MatchString(name) {
			return name
		}
	}
	return ""
}

func handleReaderDay(err error, newest bool, warn func(error)) error {
	if !errors.Is(err, errReaderTornTail) {
		return err
	}
	if !newest && warn != nil {
		warn(err)
	}
	return nil
}

func scanReaderDay(path string, filter ReaderFilter, emit func([]byte) error) error {
	f, err := openReaderDay(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := statRegular(f)
	if err != nil {
		return err
	}
	end, err := readerSnapshotEnd(f, info.Size())
	if err != nil {
		return err
	}
	if err := scanReaderLines(f, end, filter, emit); err != nil {
		return err
	}
	if end < info.Size() {
		return readerOffsetError(f, end, errReaderTornTail)
	}
	return nil
}

func openReaderDay(path string) (*os.File, error) {
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Clean(path), os.O_RDONLY|noFollow|nonBlock, 0)
}

// Only the newline-terminated prefix is stable while a writer repairs a tail.
func readerSnapshotEnd(f *os.File, size int64) (int64, error) {
	var chunk [4096]byte
	for end := size; end > 0; {
		start := max(0, end-int64(len(chunk)))
		n, err := f.ReadAt(chunk[:end-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, readerOffsetError(f, start, err)
		}
		if last := bytes.LastIndexByte(chunk[:n], '\n'); last >= 0 {
			return start + int64(last) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func scanReaderLines(f *os.File, end int64, filter ReaderFilter, emit func([]byte) error) error {
	lines := bufio.NewReaderSize(io.NewSectionReader(f, 0, end), MaxLineBytes+2)
	var offset int64
	for {
		line, err := lines.ReadSlice('\n')
		if err != nil {
			if offset == end && len(line) == 0 && errors.Is(err, io.EOF) {
				return nil
			}
			return readerOffsetError(f, offset, readerLineError(err))
		}
		if err := emitReaderRecord(line[:len(line)-1], filter, emit); err != nil {
			return readerOffsetError(f, offset, err)
		}
		offset += int64(len(line))
	}
}

func readerLineError(err error) error {
	if errors.Is(err, io.EOF) {
		return errReaderFileChanged
	}
	return endOfLines(nil, err)
}

func readerOffsetError(f *os.File, offset int64, err error) error {
	return fmt.Errorf("audit file %s at byte offset %d: %w", f.Name(), offset, err)
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

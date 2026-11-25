// Package audit stores pool audit records as one JSON object per line in
// daily files. It treats each record as opaque bytes: the record shape and
// type names belong to the shared audit contract, not to this package.
package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

	defaultLockWait = 5 * time.Second
	lockRetry       = 5 * time.Millisecond
)

var (
	dayFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)

	errLineEmpty   = errors.New("audit record is empty")
	errLineTooLong = fmt.Errorf("audit record is longer than %d bytes", MaxLineBytes)
	errLineShape   = errors.New("audit record must be one single-line JSON object")
	errTornTail    = errors.New("audit file ends in an unterminated record longer than one record")
	errLockTimeout = errors.New("timed out waiting for the audit lock, another process holds it")
)

// Store appends records to <dir>/<YYYY-MM-DD>.jsonl, one file per UTC day.
type Store struct {
	dir      string
	now      func() time.Time
	lockWait time.Duration

	mu     sync.Mutex
	synced map[string]struct{}
}

// Open creates the directory when needed and refuses one that other users can
// write to. The ownership and mode check runs on Unix only; the Windows build
// does not inspect the directory ACL. It syncs the directory and each parent
// the current user owns on every call, so a directory whose earlier sync
// failed is never trusted just because it exists.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create audit directory: %w", err)
	}
	if err := checkDirMode(dir); err != nil {
		return nil, err
	}
	if err := syncDirChain(dir); err != nil {
		return nil, fmt.Errorf("sync audit directory: %w", err)
	}
	return &Store{
		dir:      dir,
		now:      time.Now,
		lockWait: defaultLockWait,
		synced:   map[string]struct{}{},
	}, nil
}

// syncDirFunc is syncDir, replaceable so tests can inject a failure.
var syncDirFunc = syncDir

// syncDirChain syncs dir, then each parent while the directory below it is
// owned by the current user: those are the entries this process could have
// created. A parent the user does not own is synced too, but its failure is
// ignored because it may not be readable.
func syncDirChain(dir string) error {
	first := filepath.Clean(dir)
	for p := first; ; {
		owned := ownedByCurrentUser(p)
		if err := syncDirFunc(p); err != nil && (owned || p == first) {
			return err
		}
		parent := filepath.Dir(p)
		if !owned || parent == p {
			return nil
		}
		p = parent
	}
}

// Append writes one record and syncs it before returning. On any error the
// caller must refuse the action the record covers. After a sync or close
// error the record may still be on disk, so a refused action can have one.
// A crash can leave an unterminated tail, which Append cuts before writing;
// it was never acknowledged. A tail longer than one record with no newline
// makes every append for that day fail until the file is repaired by hand.
// It waits a bounded time for the directory lock and then fails.
func (s *Store) Append(record []byte) error {
	if err := checkRecord(record); err != nil {
		return err
	}
	lock, err := s.newLock()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := acquire(lock, s.lockWait, false); err != nil {
		return err
	}
	day := s.now().UTC().Format(dayLayout)
	if err := s.appendDay(day, record); err != nil {
		return err
	}
	return s.syncDayEntry(day)
}

// acquire takes the lock, polling until wait runs out.
func acquire(lock *flock.Flock, wait time.Duration, shared bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	try := lock.TryLockContext
	if shared {
		try = lock.TryRLockContext
	}
	ok, err := try(ctx, lockRetry)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (err == nil && !ok):
		return fmt.Errorf("%w (waited %s)", errLockTimeout, wait)
	case err != nil:
		return fmt.Errorf("lock audit directory: %w", err)
	}
	return nil
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

// writeRecord writes the record and its newline in one call at the end of the
// file, which is safe without O_APPEND because the caller holds the exclusive
// directory lock. A torn tail from an earlier crash is cut off first, and a
// failed or short write is cut back, so a record never joins a partial line.
// A sync error is returned after the line is written, so the record may exist
// although the caller must refuse.
func writeRecord(f *os.File, record []byte) (err error) {
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close audit file: %w", cerr)
		}
	}()
	size, err := trimTornTail(f)
	if err != nil {
		return err
	}
	if _, err := f.Seek(size, io.SeekStart); err != nil {
		return fmt.Errorf("seek audit file: %w", err)
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

// openDayFile opens the day file and reports whether this call created it. A
// new file's directory entry is not durable until the directory is synced.
func openDayFile(path string) (f *os.File, created bool, err error) {
	if err := checkRegular(path); err != nil {
		return nil, false, err
	}
	clean := filepath.Clean(path)
	f, err = os.OpenFile(clean, os.O_RDWR|os.O_CREATE|os.O_EXCL|noFollow, fileMode)
	if err == nil {
		return f, true, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, false, fmt.Errorf("open audit file: %w", err)
	}
	f, err = os.OpenFile(clean, os.O_RDWR|noFollow|nonBlock, fileMode)
	if err != nil {
		return nil, false, fmt.Errorf("open audit file: %w", err)
	}
	return f, false, nil
}

// trimTornTail removes bytes after the last newline, a record cut short by a
// crash, and returns the size that remains.
func trimTornTail(f *os.File) (int64, error) {
	info, err := statRegular(f)
	if err != nil {
		return 0, err
	}
	end, err := completeEnd(f, info.Size())
	if err != nil || end == info.Size() {
		return end, err
	}
	if err := f.Truncate(end); err != nil {
		return 0, fmt.Errorf("cut torn audit record: %w", err)
	}
	return end, nil
}

// completeEnd returns the offset just after the last newline in the first
// size bytes. A newline must lie within one record length of the end.
func completeEnd(f *os.File, size int64) (int64, error) {
	if size == 0 {
		return 0, nil
	}
	window := min(size, int64(MaxLineBytes+1))
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil {
		return 0, fmt.Errorf("read audit file tail: %w", err)
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 && size > int64(MaxLineBytes) {
		return size, errTornTail
	}
	return size - window + int64(last+1), nil
}

// statRegular checks the open file itself, which closes the gap between the
// path check and the open.
func statRegular(f *os.File) (os.FileInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat audit file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("audit path is not a regular file: %s", f.Name())
	}
	return info, nil
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
// by a crash is reported as io.ErrUnexpectedEOF. Each error names the day it
// came from and wraps the cause, including an error returned by fn.
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
			return fmt.Errorf("audit day %s: %w", day, err)
		}
	}
	return nil
}

func (s *Store) appendDay(day string, record []byte) error {
	f, created, err := openDayFile(filepath.Join(s.dir, day+fileExt))
	if created {
		s.forgetSynced(day)
	}
	if err != nil {
		return err
	}
	return writeRecord(f, record)
}

func (s *Store) forgetSynced(day string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.synced, day)
}

// syncDayEntry makes the day file's directory entry durable. It syncs the
// first time this Store appends to a day and again whenever a file was created
// or an earlier sync failed. A file sync alone does not persist a new name.
func (s *Store) syncDayEntry(day string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.synced[day]; ok {
		return nil
	}
	if err := syncDirFunc(s.dir); err != nil {
		return fmt.Errorf("sync audit directory: %w", err)
	}
	s.synced[day] = struct{}{}
	return nil
}

func (s *Store) newLock() (*flock.Flock, error) {
	return newFlock(filepath.Join(s.dir, lockName))
}

// newFlock refuses a lock path that is not a regular file and never follows
// a symlink when it opens one.
func newFlock(path string) (*flock.Flock, error) {
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	return flock.New(
		path,
		flock.SetPermissions(fileMode),
		flock.SetFlag(os.O_CREATE|os.O_RDWR|noFollow|nonBlock),
	), nil
}

type snapshot struct {
	f    *os.File
	end  int64
	torn bool
}

// scanDay fixes the snapshot under the shared lock, then releases it before
// calling fn, so a callback never blocks or deadlocks an append. The snapshot
// ends at the last newline: bytes before it never change, while an append may
// cut the unterminated tail after it. A tail left after the snapshot is
// reported as io.ErrUnexpectedEOF once the complete lines are done.
func (s *Store) scanDay(day string, fn func(string, []byte) error) error {
	snap, err := s.openSnapshot(day)
	if err != nil {
		return err
	}
	defer func() { _ = snap.f.Close() }()
	lines := io.LimitReader(snap.f, snap.end)
	if err := eachLine(lines, func(line []byte) error { return fn(day, line) }); err != nil {
		return err
	}
	if snap.torn {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (s *Store) openSnapshot(day string) (*snapshot, error) {
	lock, err := s.newLock()
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err := acquire(lock, s.lockWait, true); err != nil {
		return nil, err
	}
	path := filepath.Clean(filepath.Join(s.dir, day+fileExt))
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow|nonBlock, 0)
	if err != nil {
		return nil, fmt.Errorf("read audit file: %w", err)
	}
	snap, err := takeSnapshot(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return snap, nil
}

func takeSnapshot(f *os.File) (*snapshot, error) {
	info, err := statRegular(f)
	if err != nil {
		return nil, err
	}
	end, err := completeEnd(f, info.Size())
	if err != nil {
		return nil, err
	}
	return &snapshot{f: f, end: end, torn: end < info.Size()}, nil
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
		record := line[:len(line)-1]
		if len(record) > MaxLineBytes {
			return errLineTooLong
		}
		if ferr := fn(record); ferr != nil {
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

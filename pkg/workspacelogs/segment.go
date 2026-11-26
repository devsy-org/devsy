package workspacelogs

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	segmentPrefix = "seg-"
	segmentSuffix = ".jsonl"
	segmentDigits = 20
)

type segment struct {
	name  string
	first uint64
}

func segmentName(first uint64) string {
	return fmt.Sprintf("%s%0*d%s", segmentPrefix, segmentDigits, first, segmentSuffix)
}

func parseSegmentName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
		return 0, false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	if len(digits) != segmentDigits {
		return 0, false
	}
	first, err := strconv.ParseUint(digits, 10, 64)

	return first, err == nil
}

// listSegments returns the producer's segments ordered by first sequence.
func listSegments(dir string) ([]segment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}
	var segs []segment
	for _, e := range entries {
		if first, ok := parseSegmentName(e.Name()); ok && e.Type().IsRegular() {
			segs = append(segs, segment{name: e.Name(), first: first})
		}
	}
	slices.SortFunc(segs, func(a, b segment) int { return cmp.Compare(a.first, b.first) })

	return segs, nil
}

// scanSegment calls fn for every committed line. A malformed committed line is
// reported as a nil record. A final line without a newline is not committed
// yet and is ignored. fn returns false to stop.
func scanSegment(path string, fn func(rec *Record) bool) error {
	if err := checkRegular(path); err != nil {
		return err
	}
	f, err := os.Open(path) //nolint:gosec // path is built from validated IDs
	if err != nil {
		return fmt.Errorf("open segment: %w", err)
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}

			return fmt.Errorf("read segment: %w", readErr)
		}
		if !fn(decodeLine(line)) {
			return nil
		}
	}
}

func decodeLine(line []byte) *Record {
	var rec Record
	if err := json.Unmarshal(bytes.TrimSpace(line), &rec); err != nil || rec.Validate() != nil {
		return nil
	}

	return &rec
}

// repairTail truncates an incomplete trailing line left by a crashed writer and
// returns the resulting size.
func repairTail(path string) (int64, error) {
	if err := checkRegular(path); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, filePerm) //nolint:gosec // validated path
	if err != nil {
		return 0, fmt.Errorf("open segment: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return 0, fmt.Errorf("read segment: %w", err)
	}
	keep := int64(bytes.LastIndexByte(data, '\n') + 1)
	if keep != int64(len(data)) {
		if err = f.Truncate(keep); err != nil {
			return 0, fmt.Errorf("truncate partial record: %w", err)
		}
	}

	return keep, nil
}

type tail struct {
	name    string
	size    int64
	lastSeq uint64
}

// loadTail returns the position after the producer's last committed record.
func (s *Store) loadTail(dir string) (tail, error) {
	segs, err := listSegments(dir)
	if err != nil || len(segs) == 0 {
		return tail{}, err
	}
	last := segs[len(segs)-1]
	path := filepath.Join(dir, last.name)
	if err = checkRegular(path); err != nil {
		return tail{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return tail{}, fmt.Errorf("stat segment: %w", err)
	}
	// Our own last write left the file at a known-good size; skip the repair
	// and rescan unless another process or a crash changed it.
	if cached, ok := s.cachedTail(dir, last.name, info.Size()); ok {
		return cached, nil
	}

	return rescanTail(path, last)
}

func (s *Store) cachedTail(dir, name string, size int64) (tail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cached, ok := s.tails[dir]

	return cached, ok && cached.name == name && cached.size == size
}

// rescanTail repairs a partial final line and finds the last sequence.
func rescanTail(path string, last segment) (tail, error) {
	size, err := repairTail(path)
	if err != nil {
		return tail{}, err
	}
	t := tail{name: last.name, size: size, lastSeq: last.first - 1}
	err = scanSegment(path, func(rec *Record) bool {
		if rec != nil {
			t.lastSeq = max(t.lastSeq, rec.Sequence)
		}

		return true
	})

	return t, err
}

func (s *Store) rememberTail(dir string, t tail) {
	s.mu.Lock()
	s.tails[dir] = t
	s.mu.Unlock()
}

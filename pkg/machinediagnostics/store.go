package machinediagnostics

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Dir             string
	Reader          ReaderIdentity
	PatrolInterval  time.Duration
	MaxBytes        int
	MaxSegmentBytes int
	Now             func() time.Time
	ErrorReporter   func(error)
}
type Recorder interface {
	Record(Event)
	Update(Status)
	Close() error
}
type Store struct {
	mu                        sync.Mutex
	dir, eventsDir, sessionID string
	reader                    ReaderIdentity
	sequence                  uint64
	maxBytes, maxSegmentBytes int
	now                       func() time.Time
	errorReporter             func(error)
	lastReportedError         time.Time
}

func DiagnosticsDir(root string) string { return filepath.Join(root, "diagnostics", "daemon") }
func NewRecorder(opts Options) (*Store, error) {
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("diagnostics directory must be absolute")
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = MaxRemoteEventBytes
	}
	if opts.MaxSegmentBytes == 0 {
		opts.MaxSegmentBytes = MaxSegmentBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{dir: filepath.Clean(opts.Dir), eventsDir: filepath.Join(opts.Dir, "events"), reader: opts.Reader, maxBytes: opts.MaxBytes, maxSegmentBytes: opts.MaxSegmentBytes, now: opts.Now, errorReporter: opts.ErrorReporter, sessionID: newSessionID()}
	if err := s.ensureDir(s.dir); err != nil {
		return nil, err
	}
	if err := s.ensureDir(s.eventsDir); err != nil {
		return nil, err
	}
	return s, nil
}
func Nop() Recorder { return nopRecorder{} }

type nopRecorder struct{}

func (nopRecorder) Record(Event)   {}
func (nopRecorder) Update(Status)  {}
func (nopRecorder) Close() error   { return nil }
func (s *Store) SessionID() string { return s.sessionID }
func (s *Store) Record(event Event) {
	if err := s.record(event); err != nil {
		s.reportError(err)
	}
}
func (s *Store) record(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	event.SchemaVersion = SchemaVersion
	event.SessionID = s.sessionID
	event.Sequence = s.sequence
	if event.Timestamp.IsZero() {
		event.Timestamp = s.now().UTC()
	}
	event.Message = NewSanitizer(os.Environ()).Message(event.Message)
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path, err := s.activeSegment(len(b))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.applyOwner(path); err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	return s.prune()
}
func (s *Store) Update(status Status) {
	if err := s.update(status); err != nil {
		s.reportError(err)
	}
}
func (s *Store) update(status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status.SchemaVersion = SchemaVersion
	status.SessionID = s.sessionID
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = s.now().UTC()
	}
	status.LatestSequence = s.sequence
	b, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return s.writeAtomic(filepath.Join(s.dir, "status.json"), b)
}
func (s *Store) Close() error { return nil }
func (s *Store) reportError(err error) {
	if s.errorReporter == nil {
		return
	}
	s.mu.Lock()
	now := s.now()
	if now.Sub(s.lastReportedError) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastReportedError = now
	s.mu.Unlock()
	// Operator callbacks must never run while holding the recorder lock.
	s.errorReporter(err)
}
func (s *Store) ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	return s.applyOwner(path)
}
func (s *Store) applyOwner(path string) error {
	if s.reader.UID < 0 || s.reader.GID < 0 {
		return nil
	}
	if err := os.Chmod(path, map[bool]os.FileMode{true: 0o750, false: 0o640}[isDir(path)]); err != nil {
		return err
	}
	if err := os.Chown(path, s.reader.UID, s.reader.GID); err != nil {
		return err
	}
	return nil
}
func isDir(path string) bool { fi, err := os.Stat(path); return err == nil && fi.IsDir() }
func (s *Store) writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(s.dir, ".status-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Chmod(0o640)
	}
	if err == nil {
		err = f.Chown(s.reader.UID, s.reader.GID)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (s *Store) activeSegment(next int) (string, error) {
	entries, err := s.segments()
	if err != nil {
		return "", err
	}
	index := 1
	var path string
	if len(entries) > 0 {
		path = entries[len(entries)-1]
		var fi os.FileInfo
		fi, err = os.Stat(path)
		if err != nil {
			return "", err
		}
		if fi.Size()+int64(next) <= int64(s.maxSegmentBytes) {
			return path, nil
		}
		index = len(entries) + 1
	}
	return filepath.Join(s.eventsDir, fmt.Sprintf("%s-%06d.ndjson", s.sessionID, index)), nil
}
func (s *Store) segments() ([]string, error) {
	entries, err := os.ReadDir(s.eventsDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	prefix := s.sessionID + "-"
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".ndjson") {
			paths = append(paths, filepath.Join(s.eventsDir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}
func (s *Store) prune() error {
	entries, err := os.ReadDir(s.eventsDir)
	if err != nil {
		return err
	}
	type item struct {
		path    string
		size    int64
		modTime time.Time
	}
	var all []item
	var total int64
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		fi, er := e.Info()
		if er != nil {
			return er
		}
		p := filepath.Join(s.eventsDir, e.Name())
		all = append(all, item{path: p, size: fi.Size(), modTime: fi.ModTime()})
		total += fi.Size()
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].modTime.Equal(all[j].modTime) {
			return all[i].path < all[j].path
		}
		return all[i].modTime.Before(all[j].modTime)
	})
	for len(all) > 1 && total > int64(s.maxBytes) {
		if err := os.Remove(all[0].path); err != nil {
			return err
		}
		total -= all[0].size
		all = all[1:]
	}
	return nil
}
func newSessionID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func Read(dir, after string, limit int, interval time.Duration, now time.Time) ReadResponse {
	r := ReadResponse{SchemaVersion: SchemaVersion, Availability: AvailabilityAvailable, ObservedAt: now.UTC(), Freshness: FreshnessUnknown, Cursor: CursorInfo{State: CursorNone}}
	if limit <= 0 {
		limit = DefaultReadEvents
	}
	if limit > MaxReadEvents {
		limit = MaxReadEvents
	}
	status, err := readStatus(filepath.Join(dir, "status.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.Availability = AvailabilityNotInitialized
		} else if errors.Is(err, os.ErrPermission) {
			r.Availability = AvailabilityPermissionDenied
			r.Error = &DiagnosticError{Code: "diagnostics_permission_denied", Message: "Access to the remote diagnostics snapshot was denied.", Timestamp: now.UTC()}
		} else {
			r.Availability = AvailabilityCorrupt
			r.Error = &DiagnosticError{Code: "diagnostics_corrupt", Message: "Remote diagnostics could not be read.", Timestamp: now.UTC()}
		}
		return r
	}
	r.Status = status
	r.Freshness = freshness(status, interval, now)
	events, err := readEvents(filepath.Join(dir, "events"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			r.Availability = AvailabilityPermissionDenied
			r.Error = &DiagnosticError{Code: "diagnostics_permission_denied", Message: "Access to remote diagnostic events was denied.", Timestamp: now.UTC()}
		} else {
			r.Availability = AvailabilityCorrupt
			r.Error = &DiagnosticError{Code: "diagnostics_corrupt", Message: "Remote diagnostic events could not be read.", Timestamp: now.UTC()}
		}
		return r
	}
	c, err := decodeCursor(after)
	if err != nil {
		r.Cursor = CursorInfo{State: CursorReset, Reason: "invalid_cursor"}
	}
	if c.sessionID != "" && c.sessionID != status.SessionID {
		r.Cursor = CursorInfo{State: CursorReset, Reason: "session_changed"}
	}
	currentEvents := make([]Event, 0, len(events))
	for _, event := range events {
		if event.SessionID == status.SessionID {
			currentEvents = append(currentEvents, event)
		}
	}
	start := uint64(0)
	if c.sessionID == status.SessionID {
		start = c.sequence
		if len(currentEvents) > 0 && start < currentEvents[0].Sequence-1 {
			r.Cursor = CursorInfo{State: CursorGap, Reason: "retention"}
		}
	}
	for _, e := range currentEvents {
		if e.Sequence > start {
			r.Events = append(r.Events, e)
			if len(r.Events) >= limit {
				break
			}
		}
	}
	if len(r.Events) > 0 {
		r.Cursor.Next = EncodeCursor(status.SessionID, r.Events[len(r.Events)-1].Sequence)
		if r.Cursor.State == CursorNone {
			r.Cursor.State = CursorOK
		}
	}
	return r
}
func readStatus(path string) (*Status, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.SchemaVersion != SchemaVersion || s.SessionID == "" {
		return nil, fmt.Errorf("invalid diagnostics status")
	}
	return &s, nil
}
func readEvents(dir string) ([]Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".ndjson") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	var events []Event
	for _, p := range paths {
		content, er := os.ReadFile(p)
		if er != nil {
			if errors.Is(er, os.ErrNotExist) {
				continue
			}
			return nil, er
		}
		lines := bytes.Split(content, []byte{'\n'})
		for index, line := range lines {
			if len(line) == 0 {
				continue
			}
			var e Event
			if er := json.Unmarshal(line, &e); er != nil {
				// An incomplete final append is the only malformed record that a
				// reader may ignore. A newline makes every earlier record complete.
				if index == len(lines)-1 && !bytes.HasSuffix(content, []byte{'\n'}) {
					break
				}
				return nil, fmt.Errorf("decode diagnostic event %s record %d: %w", p, index+1, er)
			}
			events = append(events, e)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events, nil
}
func freshness(status *Status, interval time.Duration, now time.Time) Freshness {
	if status == nil || status.UpdatedAt.IsZero() {
		return FreshnessUnknown
	}
	threshold := 3 * interval
	if threshold < 90*time.Second {
		threshold = 90 * time.Second
	}
	if now.Sub(status.UpdatedAt) > threshold {
		return FreshnessStale
	}
	return FreshnessFresh
}

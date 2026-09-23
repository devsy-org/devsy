package workspacejournal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/gofrs/flock"
)

const (
	SchemaVersion          = 1
	DefaultMaxBytes        = 5 * 1024 * 1024
	DefaultMaxSegmentBytes = 512 * 1024
	DefaultLimit           = 100
	MaxLimit               = 1000
	// Records may occupy a complete segment, but never more than the default
	// segment size so readers can enforce a finite bound without journal
	// configuration.
	maxRecordBytes       = DefaultMaxSegmentBytes
	appendLockName       = "append.lock"
	appendLockTimeout    = 15 * time.Second
	appendLockRetryDelay = 50 * time.Millisecond
)

type Event struct {
	SchemaVersion     int               `json:"schemaVersion"`
	Timestamp         time.Time         `json:"timestamp"`
	WorkspaceID       string            `json:"workspaceId,omitempty"`
	Pipeline          status.Pipeline   `json:"pipeline"`
	OperationID       string            `json:"operationId,omitempty"`
	ParentOperationID string            `json:"parentOperationId,omitempty"`
	Phase             status.Phase      `json:"phase"`
	Step              string            `json:"step,omitempty"`
	State             status.State      `json:"state"`
	DurationMillis    int64             `json:"durationMillis,omitempty"`
	Error             *status.ErrorInfo `json:"error,omitempty"`
}

type Options struct {
	Dir                       string
	MaxBytes, MaxSegmentBytes int
	Now                       func() time.Time
	Env                       []string
}
type Journal struct {
	mu                        sync.Mutex
	dir                       string
	maxBytes, maxSegmentBytes int
	now                       func() time.Time
	redactor                  *secrets.Redactor
}

func DefaultDir() (string, error) {
	root, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", "workspace-events"), nil
}

func New(opts Options) (*Journal, error) {
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("journal directory must be absolute")
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.MaxSegmentBytes == 0 {
		opts.MaxSegmentBytes = DefaultMaxSegmentBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(opts.Dir, 0o700); err != nil { //nolint:gosec
		return nil, err
	}
	return &Journal{
		dir:             opts.Dir,
		maxBytes:        opts.MaxBytes,
		maxSegmentBytes: opts.MaxSegmentBytes,
		now:             opts.Now,
		redactor:        secrets.NewEnvironmentRedactor(opts.Env),
	}, nil
}

func OpenDefault() (*Journal, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return New(Options{Dir: dir})
}

func (j *Journal) Reporter(workspaceID string) status.Reporter {
	return reporter{journal: j, workspaceID: workspaceID}
}

type reporter struct {
	journal     *Journal
	workspaceID string
}

// afterAppendLockAcquired, when set, runs at the start of the serialized
// append transaction. Tests use it to hold one instance mid-transaction and
// prove other instances block on the lock.
var afterAppendLockAcquired func()

func (r reporter) Report(e status.Event) { _ = r.journal.Append(r.workspaceID, e) }
func (j *Journal) Append(workspaceID string, e status.Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	event := Event{
		SchemaVersion:     SchemaVersion,
		Timestamp:         j.now().UTC(),
		WorkspaceID:       workspaceID,
		Pipeline:          e.Pipeline,
		OperationID:       e.OperationID,
		ParentOperationID: e.ParentOperationID,
		Phase:             e.Phase,
		Step:              j.redactor.Redact(e.Step),
		State:             e.State,
		DurationMillis:    e.Duration.Milliseconds(),
		Error:             redactError(j.redactor, e.Error),
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	limit := min(j.maxSegmentBytes, maxRecordBytes)
	if len(b) > limit {
		log.Debugf(
			"workspace journal: skipping oversized event (%d bytes, record limit %d)",
			len(b), limit,
		)
		return nil
	}
	unlock, err := j.acquireAppendLock()
	if err != nil {
		return err
	}
	defer unlock()
	if afterAppendLockAcquired != nil {
		afterAppendLockAcquired()
	}
	return j.appendLocked(b)
}

// acquireAppendLock takes an exclusive cross-process lock on the journal
// directory and returns a release func that is always safe to call. It
// serializes the append transaction (segment selection, write, prune) across
// Journal instances in every process, so concurrent appends cannot drop an
// event to a prune race or exceed maxSegmentBytes. It gives up with a clear
// error after appendLockTimeout rather than blocking forever behind a stuck
// holder. j.mu still guards in-process goroutine safety and is always taken
// before this lock.
//
// NOT reentrant: each call opens its own flock handle, so taking the lock
// while already holding it on the same directory self-deadlocks.
func (j *Journal) acquireAppendLock() (func(), error) {
	l := flock.New(filepath.Join(j.dir, appendLockName))
	ctx, cancel := context.WithTimeout(context.Background(), appendLockTimeout)
	defer cancel()
	locked, err := l.TryLockContext(ctx, appendLockRetryDelay)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", appendLockName, err)
	}
	if !locked {
		return nil, fmt.Errorf("lock %s: timed out after %s", appendLockName, appendLockTimeout)
	}
	return func() { _ = l.Unlock() }, nil
}

// appendLocked writes one event record and prunes. Callers must hold the
// append lock.
func (j *Journal) appendLocked(b []byte) error {
	path, err := j.activeSegment(len(b))
	if err != nil {
		return err
	}
	// #nosec G304 -- Segment paths are generated inside the private journal directory.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err = os.Chmod(path, 0o600); err == nil {
		_, err = f.Write(b)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return j.prune()
}

func redactError(r *secrets.Redactor, in *status.ErrorInfo) *status.ErrorInfo {
	if in == nil {
		return nil
	}
	out := &status.ErrorInfo{Code: in.Code, Message: r.Redact(in.Message), Hint: r.Redact(in.Hint)}
	if in.Context != nil {
		out.Context = map[string]string{}
		for k, v := range in.Context {
			out.Context[k] = r.Redact(v)
		}
	}
	return out
}

func (j *Journal) activeSegment(next int) (string, error) {
	paths, err := segmentPaths(j.dir)
	if err != nil {
		return "", err
	}
	if len(paths) > 0 {
		last := paths[len(paths)-1]
		fi, er := os.Stat(last)
		if er != nil {
			return "", er
		}
		if fi.Size()+int64(next) <= int64(j.maxSegmentBytes) {
			return last, nil
		}
	}
	return filepath.Join(j.dir, fmt.Sprintf("events-%06d.ndjson", nextSegmentNumber(paths))), nil
}

func nextSegmentNumber(paths []string) int {
	if len(paths) == 0 {
		return 1
	}
	maxNumber := 0
	for _, path := range paths {
		name := filepath.Base(path)
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "events-"), ".ndjson"))
		if err == nil && n > maxNumber {
			maxNumber = n
		}
	}
	return maxNumber + 1
}

func segmentPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() &&
			strings.HasPrefix(name, "events-") &&
			strings.HasSuffix(name, ".ndjson") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (j *Journal) prune() error {
	paths, err := segmentPaths(j.dir)
	if err != nil {
		return err
	}
	var total int64
	sizes := make([]int64, len(paths))
	for i, p := range paths {
		fi, er := os.Stat(p)
		if er != nil {
			return er
		}
		sizes[i] = fi.Size()
		total += fi.Size()
	}
	for i := 0; i < len(paths)-1 && total > int64(j.maxBytes); i++ {
		if err := os.Remove(paths[i]); err != nil {
			return err
		}
		total -= sizes[i]
	}
	return nil
}

func Read(dir, workspaceID string, limit int) ([]Event, error) {
	paths, err := segmentPaths(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	events, err := readSegments(paths, workspaceID)
	if err != nil {
		return nil, err
	}
	return recentEvents(events, limit), nil
}

func readSegments(paths []string, workspaceID string) ([]Event, error) {
	var events []Event
	for _, path := range paths {
		segmentEvents, err := readSegment(path, workspaceID)
		if errors.Is(err, os.ErrNotExist) {
			// A concurrent prune removed the segment after listing.
			continue
		}
		if err != nil {
			return nil, err
		}
		events = append(events, segmentEvents...)
	}
	return events, nil
}

func readSegment(path, workspaceID string) ([]Event, error) {
	// #nosec G304 -- Segment paths come from the private journal directory listing.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var events []Event
	reader := bufio.NewReaderSize(file, maxRecordBytes)
	for {
		line, readErr := readRecord(reader)
		if len(line) > 0 {
			if event, ok := decodeEvent(line, workspaceID); ok {
				events = append(events, event)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return events, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

// readRecord returns the next journal line. Oversized records are discarded
// through their next newline so a large write cannot abort the whole read.
func readRecord(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if !errors.Is(err, bufio.ErrBufferFull) {
		return line, err
	}
	for errors.Is(err, bufio.ErrBufferFull) {
		_, err = reader.ReadSlice('\n')
	}
	return nil, err
}

func decodeEvent(data []byte, workspaceID string) (Event, bool) {
	var event Event
	if json.Unmarshal(data, &event) != nil || event.SchemaVersion != SchemaVersion {
		return Event{}, false
	}
	if workspaceID != "" && event.WorkspaceID != workspaceID {
		return Event{}, false
	}
	if !event.complete() {
		return Event{}, false
	}
	return event, true
}

func (e Event) complete() bool {
	return !e.Timestamp.IsZero() &&
		validPipeline(e.Pipeline) &&
		validPhase(e.Phase) &&
		status.ValidState(e.State)
}

func validPipeline(pipeline status.Pipeline) bool {
	switch pipeline {
	case status.PipelineWorkspaceUp, status.PipelineProvider:
		return true
	default:
		return false
	}
}

func validPhase(phase status.Phase) bool {
	switch phase {
	case status.PhaseCloningRepository,
		status.PhaseResolvingConfig,
		status.PhaseInitializeCommand,
		status.PhaseBuildingImage,
		status.PhaseStartingContainer,
		status.PhaseInjectingAgent,
		status.PhaseRunningLifecycleHook,
		status.PhaseWaitingFor,
		status.PhaseRunningCommand,
		status.PhaseConfiguringWorkspace,
		status.PhaseConfiguringSSH,
		status.PhaseStartingSSHTunnel,
		status.PhaseLaunchingIDE,
		status.PhaseStoppingWorkspace,
		status.PhaseDeletingWorkspace,
		status.PhaseRebuildingWorkspace,
		status.PhaseResettingWorkspace,
		status.PhaseImportingWorkspace,
		status.PhaseReady,
		status.PhaseFailed,
		status.PhaseInstallingProvider,
		status.PhaseResolvingOptions,
		status.PhaseRunningInit:
		return true
	default:
		return false
	}
}

func recentEvents(events []Event, limit int) []Event {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	if len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

func Last(dir, workspaceID string) (*Event, error) {
	events, err := Read(dir, workspaceID, MaxLimit)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	for _, event := range slices.Backward(events) {
		if event.State == status.StateSucceeded || event.State == status.StateFailed {
			return &event, nil
		}
	}
	e := events[len(events)-1]
	return &e, nil
}

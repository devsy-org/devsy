package workspacelogs

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

// Page limits.
const (
	DefaultReadLimit  = 500
	MaxReadLimit      = 2000
	DefaultIndexLimit = 20
	MaxIndexLimit     = 100

	// envelopeReserve is page space kept for the response around the records.
	envelopeReserve = 4 * 1024
)

// ReadRequest selects one page of a run.
type ReadRequest struct {
	RunID        string
	Hook         string
	InvocationID string
	// After is the NextCursor of the previous page.
	After string
	// Limit is the most records in the page. Zero means DefaultReadLimit.
	Limit int
	// MaxBytes caps the encoded page. Zero or less means MaxPageBytes.
	MaxBytes int
}

// SourceAvailability tells whether a producer's records can be read.
type SourceAvailability struct {
	ProducerID string `json:"producerId"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
}

// ReadResponse is one bounded page.
type ReadResponse struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Run           RunSummary           `json:"run"`
	Records       []Record             `json:"records"`
	NextCursor    string               `json:"nextCursor,omitempty"`
	HasMore       bool                 `json:"hasMore"`
	Availability  []SourceAvailability `json:"availability,omitempty"`
	Complete      bool                 `json:"complete"`
}

// Read returns the next page of a run. Output is read from disk in pages;
// the archive is never loaded whole.
func (s *Store) Read(req ReadRequest) (*ReadResponse, error) {
	limit, maxBytes, err := readLimits(req)
	if err != nil {
		return nil, err
	}
	plan, err := s.prepareRead(req)
	if err != nil {
		return nil, err
	}
	pg := newPage(limit, maxBytes-envelopeReserve, plan.cur.Pos)
	pg.gaps = plan.cur.Gaps
	for _, p := range plan.m.Producers {
		if err = s.readProducer(plan.m.RunID, p.ProducerID, plan.f, pg); err != nil {
			return nil, err
		}
		if pg.full {
			break
		}
	}

	return s.response(plan.m, plan.f, pg)
}

type readPlan struct {
	m   *RunManifest
	f   *filter
	cur *cursor
}

// prepareRead resolves the run, filter and start position, and rejects a
// cursor bound to a different run or filter.
func (s *Store) prepareRead(req ReadRequest) (*readPlan, error) {
	m, err := s.Manifest(req.RunID)
	if err != nil {
		return nil, err
	}
	f, err := s.newFilter(m, req)
	if err != nil {
		return nil, err
	}
	cur, err := decodeCursor(req.After, cursorRun)
	if err != nil {
		return nil, err
	}
	if req.After != "" && (cur.Run != req.RunID || cur.Filter != f.hash) {
		return nil, fmt.Errorf(
			"%w: cursor does not match the selected run or filters",
			ErrInvalidCursor,
		)
	}

	return &readPlan{m: m, f: f, cur: cur}, nil
}

func readLimits(req ReadRequest) (limit, maxBytes int, err error) {
	limit, maxBytes = req.Limit, req.MaxBytes
	if limit == 0 {
		limit = DefaultReadLimit
	}
	if maxBytes <= 0 {
		maxBytes = MaxPageBytes
	}
	if limit < 0 || limit > MaxReadLimit {
		return 0, 0, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			ErrInvalidSelector,
			MaxReadLimit,
		)
	}
	if maxBytes > MaxPageBytes || maxBytes <= envelopeReserve {
		return 0, 0, fmt.Errorf("%w: invalid byte cap", ErrInvalidSelector)
	}

	return limit, maxBytes, nil
}

func (s *Store) response(m *RunManifest, f *filter, pg *page) (*ReadResponse, error) {
	resp := &ReadResponse{
		SchemaVersion: SchemaVersion,
		Run:           m.Summary(),
		Records:       pg.records,
		HasMore:       pg.full,
		Complete:      m.Complete() && !pg.full && !pg.gaps,
	}
	if resp.Records == nil {
		resp.Records = []Record{}
	}
	for _, p := range m.Producers {
		a := SourceAvailability{ProducerID: p.ProducerID, Available: p.State != ProducerUnavailable}
		if !a.Available {
			a.Reason = string(ConditionSourceUnavailable)
		}
		resp.Availability = append(resp.Availability, a)
	}
	token, err := (&cursor{
		Version: SchemaVersion, Kind: cursorRun, Run: m.RunID, Filter: f.hash, Pos: pg.pos, Gaps: pg.gaps,
	}).encode()
	if err != nil {
		return nil, err
	}
	resp.NextCursor = token

	return resp, nil
}

// filter selects records by hook or invocation. Invocation membership is
// resolved up front from the invocation records so a resumed page filters
// the same way as the first.
type filter struct {
	active  bool
	hash    string
	allowed map[string]bool
}

func (s *Store) newFilter(m *RunManifest, req ReadRequest) (*filter, error) {
	f := &filter{hash: filterHash(req.Hook, req.InvocationID)}
	if req.Hook == "" && req.InvocationID == "" {
		return f, nil
	}
	if err := validateFilterSelectors(req); err != nil {
		return nil, err
	}
	hooks, err := s.invocationHooks(m)
	if err != nil {
		return nil, err
	}
	f.active, f.allowed = true, matchInvocations(hooks, req)
	if req.InvocationID != "" && !f.allowed[req.InvocationID] {
		return nil, fmt.Errorf("%w: invocation is not part of this run", ErrNotFound)
	}

	return f, nil
}

func validateFilterSelectors(req ReadRequest) error {
	if req.Hook != "" && !ValidHook(req.Hook) {
		return fmt.Errorf("%w: unknown hook %q", ErrInvalidSelector, req.Hook)
	}
	if req.InvocationID != "" {
		return requireID("invocationId", req.InvocationID)
	}

	return nil
}

// matchInvocations returns the invocation IDs that satisfy every set selector.
func matchInvocations(hooks map[string]string, req ReadRequest) map[string]bool {
	allowed := map[string]bool{}
	for id, hook := range hooks {
		if (req.Hook == "" || hook == req.Hook) &&
			(req.InvocationID == "" || id == req.InvocationID) {
			allowed[id] = true
		}
	}

	return allowed
}

func (f *filter) accept(rec *Record) bool {
	if !f.active || rec.Kind == KindCapture {
		return true
	}
	if rec.Kind == KindProducer {
		return false
	}

	return f.allowed[rec.InvocationID]
}

// invocationHooks maps every invocation ID in the run to its hook name.
func (s *Store) invocationHooks(m *RunManifest) (map[string]string, error) {
	hooks := map[string]string{}
	for _, p := range m.Producers {
		err := s.eachRecord(m.RunID, p.ProducerID, 1, func(rec *Record) bool {
			if rec != nil && rec.Kind == KindInvocation {
				hooks[rec.InvocationID] = rec.Hook
			}

			return true
		})
		if err != nil {
			return nil, err
		}
	}

	return hooks, nil
}

// eachRecord streams a producer's committed records from sequence from on. A
// nil record stands for a malformed committed line.
func (s *Store) eachRecord(runID, producerID string, from uint64, fn func(*Record) bool) error {
	dir, err := s.producerDir(runID, producerID)
	if err != nil {
		return err
	}
	segs, err := listSegments(dir)
	if err != nil {
		return err
	}
	for _, seg := range segs[firstSegmentFor(segs, from):] {
		stopped, scanErr := scanFrom(filepath.Join(dir, seg.name), seg.first, from, fn)
		if scanErr != nil || stopped {
			return scanErr
		}
	}

	return nil
}

// firstSegmentFor returns the index of the segment holding sequence from.
func firstSegmentFor(segs []segment, from uint64) int {
	start := 0
	for i, seg := range segs {
		if seg.first <= from {
			start = i
		}
	}

	return start
}

// scanFrom passes fn the records of one segment at or after sequence from and
// reports whether fn stopped the scan. Lines are contiguous in a segment, so a
// malformed line's sequence is the one after the previous line.
func scanFrom(path string, first, from uint64, fn func(*Record) bool) (bool, error) {
	stopped := false
	implied := first
	err := scanSegment(path, func(rec *Record) bool {
		seq := implied
		if rec != nil {
			seq = rec.Sequence
		}
		implied = seq + 1
		if seq < from {
			return true
		}
		stopped = !fn(rec)

		return !stopped
	})

	return stopped, err
}

func (s *Store) readProducer(runID, producerID string, f *filter, pg *page) error {
	return s.eachRecord(runID, producerID, pg.pos[producerID]+1, func(rec *Record) bool {
		return pg.consume(runID, producerID, rec, f)
	})
}

// page accumulates records for one response.
type page struct {
	limit    int
	maxBytes int
	used     int
	records  []Record
	pos      map[string]uint64
	full     bool
	gaps     bool
}

func newPage(limit, maxBytes int, pos map[string]uint64) *page {
	next := make(map[string]uint64, len(pos))
	maps.Copy(next, pos)

	return &page{limit: limit, maxBytes: maxBytes, pos: next}
}

func (p *page) add(rec *Record) bool {
	data, err := json.Marshal(rec)
	if err != nil || len(p.records) >= p.limit ||
		(len(p.records) > 0 && p.used+len(data)+1 > p.maxBytes) {
		p.full = true

		return false
	}
	p.used += len(data) + 1
	p.records = append(p.records, *rec)

	return true
}

// consume adds one record, first reporting any missing sequence range, and
// advances the producer position. It returns false once the page is full.
func (p *page) consume(runID, producerID string, rec *Record, f *filter) bool {
	next := p.pos[producerID] + 1
	if rec == nil {
		return p.consumeMalformed(runID, producerID, next)
	}
	if rec.Sequence > next {
		gap := capture(
			Record{RunID: runID, ProducerID: producerID, Sequence: next},
			ConditionRetentionGap,
			fmt.Sprintf("sequences %d-%d are not retained", next, rec.Sequence-1),
		)
		if !p.add(&gap) {
			return false
		}
		p.gaps = true
		p.pos[producerID] = rec.Sequence - 1
	}
	if f.accept(rec) && !p.add(rec) {
		return false
	}
	p.pos[producerID] = rec.Sequence

	return true
}

func (p *page) consumeMalformed(runID, producerID string, seq uint64) bool {
	rec := capture(Record{RunID: runID, ProducerID: producerID, Sequence: seq},
		ConditionMalformedRecord, "a stored record could not be decoded")
	if !p.add(&rec) {
		return false
	}
	p.gaps = true
	p.pos[producerID] = seq

	return true
}

// capture turns where (run, producer, sequence) into a synthetic capture
// record. It is built at read time and never stored.
func capture(where Record, c Condition, detail string) Record {
	where.SchemaVersion, where.Kind = SchemaVersion, KindCapture
	where.Condition, where.Detail = c, detail

	return where
}

// IndexRequest selects one page of the run index.
type IndexRequest struct {
	Context string
	// WorkspaceUID narrows the index to one workspace generation.
	WorkspaceUID string
	After        string
	// Limit is the most runs in the page. Zero means DefaultIndexLimit.
	Limit int
}

// IndexResponse is one page of runs, newest first by (createdAt, runId).
type IndexResponse struct {
	SchemaVersion int          `json:"schemaVersion"`
	Runs          []RunSummary `json:"runs"`
	NextCursor    string       `json:"nextCursor,omitempty"`
	HasMore       bool         `json:"hasMore"`
}

// ListRuns returns retained runs for a context, including runs whose workspace
// no longer exists.
func (s *Store) ListRuns(req IndexRequest) (*IndexResponse, error) {
	limit, cur, err := indexSelectors(req)
	if err != nil {
		return nil, err
	}
	var matches []*RunManifest
	for _, r := range s.loadRuns() {
		if inIndex(r.manifest, req) && (req.After == "" || olderThan(r.manifest, cur)) {
			matches = append(matches, r.manifest)
		}
	}

	return indexPage(matches, req, limit)
}

func indexLimit(req IndexRequest) (int, error) {
	limit := req.Limit
	if limit == 0 {
		limit = DefaultIndexLimit
	}
	if req.Context == "" || limit < 0 || limit > MaxIndexLimit {
		return 0, fmt.Errorf(
			"%w: context and a limit up to %d are required", ErrInvalidSelector, MaxIndexLimit,
		)
	}

	return limit, nil
}

// indexSelectors validates the index request and decodes its cursor.
func indexSelectors(req IndexRequest) (int, *cursor, error) {
	limit, err := indexLimit(req)
	if err != nil {
		return 0, nil, err
	}
	cur, err := decodeCursor(req.After, cursorIndex)
	if err != nil {
		return 0, nil, err
	}
	if req.After != "" && (cur.Context != req.Context || cur.UID != req.WorkspaceUID) {
		return 0, nil, fmt.Errorf(
			"%w: cursor does not match the selected context or workspace",
			ErrInvalidCursor,
		)
	}

	return limit, cur, nil
}

func inIndex(m *RunManifest, req IndexRequest) bool {
	if m.Context != req.Context {
		return false
	}

	return req.WorkspaceUID == "" || (m.WorkspaceUID != nil && *m.WorkspaceUID == req.WorkspaceUID)
}

// olderThan reports whether m sorts after the cursor position.
func olderThan(m *RunManifest, cur *cursor) bool {
	if c := m.CreatedAt.Compare(cur.At); c != 0 {
		return c < 0
	}

	return m.RunID < cur.ID
}

func indexPage(matches []*RunManifest, req IndexRequest, limit int) (*IndexResponse, error) {
	slices.SortFunc(matches, func(a, b *RunManifest) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		if a.RunID > b.RunID {
			return -1
		}
		if a.RunID < b.RunID {
			return 1
		}

		return 0
	})
	resp := &IndexResponse{SchemaVersion: SchemaVersion, Runs: []RunSummary{}}
	if len(matches) > limit {
		resp.HasMore = true
		matches = matches[:limit]
	}
	for _, m := range matches {
		resp.Runs = append(resp.Runs, m.Summary())
	}
	if resp.HasMore {
		last := matches[len(matches)-1]
		token, err := (&cursor{
			Version: SchemaVersion, Kind: cursorIndex, Context: req.Context, UID: req.WorkspaceUID,
			At: last.CreatedAt, ID: last.RunID,
		}).encode()
		if err != nil {
			return nil, err
		}
		resp.NextCursor = token
	}

	return resp, nil
}

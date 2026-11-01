package workspacelogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func decodeReadResponse(data []byte) (*ReadResponse, error) {
	var resp ReadResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrInvalidRecord, err)
	}
	if resp.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema version %d", ErrInvalidRecord, resp.SchemaVersion)
	}

	return &resp, nil
}

// LocalPositions returns the last committed sequence of every producer of the
// run. A refresh asks the remote side only for records after these positions.
func (s *Store) LocalPositions(runID string) (map[string]uint64, error) {
	m, err := s.Manifest(runID)
	if err != nil {
		return nil, err
	}
	pos := make(map[string]uint64, len(m.Producers))
	for _, p := range m.Producers {
		last, lastErr := s.lastCommitted(runID, p.ProducerID)
		if lastErr != nil {
			return nil, lastErr
		}
		pos[p.ProducerID] = last
	}

	return pos, nil
}

// lastCommitted scans the producer's last segment without repairing it, so a
// reader never writes.
func (s *Store) lastCommitted(runID, producerID string) (uint64, error) {
	dir, err := s.producerDir(runID, producerID)
	if err != nil {
		return 0, err
	}
	segs, err := listSegments(dir)
	if err != nil || len(segs) == 0 {
		return 0, err
	}
	last := segs[len(segs)-1]
	path := filepath.Join(dir, last.name)
	if _, statErr := os.Lstat(path); statErr != nil {
		return 0, fmt.Errorf("stat segment: %w", statErr)
	}
	seq := last.first - 1
	err = scanSegment(path, func(rec *Record) bool {
		if rec != nil {
			seq = max(seq, rec.Sequence)
		}

		return true
	})

	return seq, err
}

// PositionCursor returns an unfiltered run cursor that resumes after pos.
func PositionCursor(runID string, pos map[string]uint64) (string, error) {
	return (&cursor{
		Version: SchemaVersion, Kind: cursorRun, Run: runID,
		Filter: filterHash("", ""), Pos: pos,
	}).encode()
}

// ImportRequest is one validated remote page to merge into the local store.
type ImportRequest struct {
	// RunID, Context and WorkspaceUID are the identity the caller expects. The
	// response must match all of them.
	RunID        string
	Context      string
	WorkspaceUID string
	Response     *ReadResponse
}

// ImportResult reports what a merge changed.
type ImportResult struct {
	Written    int
	Duplicates int
	// Stalled lists producers whose records could not be merged past a gap or
	// a malformed remote record. Their remaining records are not imported.
	Stalled []string
}

// ImportRemote merges a remote page into the local run. Records are
// deduplicated by producer and sequence. It rejects a response for another
// run, context or workspace generation, oversized pages and invalid records.
// Nothing is written when validation fails.
func (s *Store) ImportRemote(req ImportRequest) (ImportResult, error) {
	m, err := s.Manifest(req.RunID)
	if err != nil {
		return ImportResult{}, err
	}
	if err = validateImport(m, req); err != nil {
		return ImportResult{}, err
	}
	groups, order := groupByProducer(req.Response.Records)
	var res ImportResult
	for _, producerID := range order {
		stalled, mergeErr := s.importProducer(m, producerID, groups[producerID], &res)
		if mergeErr != nil {
			return res, mergeErr
		}
		if stalled {
			res.Stalled = append(res.Stalled, producerID)
		}
	}

	return res, nil
}

func (s *Store) importProducer(
	m *RunManifest, producerID string, records []Record, res *ImportResult,
) (bool, error) {
	if i := m.producer(producerID); i >= 0 && m.Producers[i].Location != LocationContainer {
		return s.stall(m.RunID, nil, ConditionMalformedRecord)
	}

	return s.mergeProducer(m.RunID, producerID, records, res)
}

func validateImport(m *RunManifest, req ImportRequest) error {
	resp := req.Response
	if resp == nil || resp.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: missing or unsupported response", ErrInvalidRecord)
	}
	if err := validateResponseIdentity(m, req); err != nil {
		return err
	}
	if len(resp.Records) > MaxReadLimit {
		return fmt.Errorf("%w: page has too many records", ErrInvalidRecord)
	}
	for i := range resp.Records {
		if err := validateImportRecord(&resp.Records[i], req.RunID); err != nil {
			return err
		}
	}

	return nil
}

func validateResponseIdentity(m *RunManifest, req ImportRequest) error {
	run := req.Response.Run
	if run.RunID != req.RunID || run.Context != req.Context {
		return fmt.Errorf("%w: response is for another run or context", ErrIdentityConflict)
	}
	if err := checkRunIdentity(m, req.Context, req.WorkspaceUID); err != nil {
		return err
	}
	if run.WorkspaceUID != nil && *run.WorkspaceUID != req.WorkspaceUID {
		return fmt.Errorf("%w: response is for another workspace", ErrIdentityConflict)
	}

	return nil
}

func validateImportRecord(rec *Record, runID string) error {
	if rec.RunID != runID {
		return fmt.Errorf("%w: record belongs to another run", ErrIdentityConflict)
	}
	if isSynthetic(rec) {
		return nil
	}

	return rec.Validate()
}

// isSynthetic reports whether rec is a capture note built at read time (a
// retention gap or a malformed line). Stored records always carry a time.
func isSynthetic(rec *Record) bool {
	return rec.Kind == KindCapture && rec.Time.IsZero()
}

func groupByProducer(records []Record) (map[string][]Record, []string) {
	groups := map[string][]Record{}
	var order []string
	for i := range records {
		id := records[i].ProducerID
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], records[i])
	}

	return groups, order
}

// mergeProducer appends the contiguous stored prefix of one producer's
// records. A synthetic note ends the prefix because its sequence has no stored
// record to merge.
func (s *Store) mergeProducer(
	runID, producerID string, records []Record, res *ImportResult,
) (bool, error) {
	if err := requireID("producerId", producerID); err != nil {
		return false, err
	}
	batch, cond := storedPrefix(records)
	if err := s.ensureProducer(runID, producerID, records[0]); err != nil {
		return s.stall(runID, err, ConditionSourceUnavailable)
	}
	if len(batch) > 0 {
		out, err := s.Append(batch)
		res.Written += out.Written
		res.Duplicates += out.Duplicates
		if err != nil {
			return s.stallOnAppendError(runID, err)
		}
	}
	if cond != "" {
		return s.stall(runID, nil, cond)
	}

	return false, nil
}

func (s *Store) stallOnAppendError(runID string, err error) (bool, error) {
	switch {
	case errors.Is(err, ErrSequenceGap):
		return s.stall(runID, err, ConditionRetentionGap)
	case errors.Is(err, ErrCapacity):
		return s.stall(runID, err, ConditionCapacity)
	case errors.Is(err, ErrInvalidRecord):
		return s.stall(runID, err, ConditionMalformedRecord)
	default:
		return false, err
	}
}

// storedPrefix returns the records before the first synthetic note and the
// condition that note stands for.
func storedPrefix(records []Record) ([]Record, Condition) {
	for i := range records {
		if isSynthetic(&records[i]) {
			return records[:i], records[i].Condition
		}
	}

	return records, ""
}

func (s *Store) ensureProducer(runID, producerID string, first Record) error {
	ref := ProducerRef{ProducerID: producerID, State: ProducerRunning, Location: LocationContainer}
	if first.Kind == KindProducer {
		ref.ParentProducerID = first.ParentProducerID
	}

	return s.RegisterProducer(runID, ref)
}

func (s *Store) stall(runID string, cause error, c Condition) (bool, error) {
	if err := s.AddCondition(runID, c); err != nil {
		return true, errors.Join(cause, err)
	}

	return true, nil
}

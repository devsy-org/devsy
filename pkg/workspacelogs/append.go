package workspacelogs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppendResult reports what an Append call persisted.
type AppendResult struct {
	// Accepted is the highest contiguous sequence persisted for the producer.
	// Acknowledge this value, never a higher one.
	Accepted   uint64
	Written    int
	Duplicates int
	Elided     int
}

// Append persists a batch of records from one producer. Records at or below
// the producer's last persisted sequence are duplicates and are skipped, so a
// retransmitted batch never repeats output. A batch that skips ahead of the
// next expected sequence returns ErrSequenceGap with Accepted unchanged.
func (s *Store) Append(records []Record) (AppendResult, error) {
	if len(records) == 0 {
		return AppendResult{}, nil
	}
	if err := validateBatch(records); err != nil {
		return AppendResult{}, err
	}
	dir, err := s.prepareProducer(&records[0])
	if err != nil {
		return AppendResult{}, err
	}

	var (
		res     AppendResult
		applied []Record
	)
	lockErr := withLock(filepath.Join(dir, appendLock), func() error {
		var appendErr error
		res, applied, appendErr = s.appendLocked(dir, records)

		return appendErr
	})
	// Producer and capture records that reached disk still update the manifest
	// when a later record in the batch failed.
	if applyErr := s.applyToManifest(records[0].RunID, applied); lockErr == nil {
		lockErr = applyErr
	}

	return res, lockErr
}

// prepareProducer makes sure the producer is registered (a mirrored producer
// may introduce itself) and returns its directory.
func (s *Store) prepareProducer(first *Record) (string, error) {
	m, err := s.Manifest(first.RunID)
	if err != nil {
		return "", err
	}
	if m.producer(first.ProducerID) < 0 {
		if err = s.registerFromFirstRecord(first); err != nil {
			return "", err
		}
	}
	if err = s.ensureDir(runsDirName, first.RunID, producersDir, first.ProducerID); err != nil {
		return "", err
	}

	return s.producerDir(first.RunID, first.ProducerID)
}

func validateBatch(records []Record) error {
	first := records[0]
	for i := range records {
		rec := &records[i]
		if err := rec.Validate(); err != nil {
			return err
		}
		if rec.RunID != first.RunID || rec.ProducerID != first.ProducerID {
			return fmt.Errorf("%w: batch mixes runs or producers", ErrInvalidRecord)
		}
		if rec.Sequence != first.Sequence+uint64(i) {
			return fmt.Errorf("%w: batch sequences are not contiguous", ErrInvalidRecord)
		}
	}

	return nil
}

func (s *Store) appendLocked(dir string, records []Record) (AppendResult, []Record, error) {
	t, err := s.loadTail(dir)
	if err != nil {
		return AppendResult{}, nil, err
	}
	runDir := filepath.Dir(filepath.Dir(dir))
	w := &batchWriter{dir: dir, t: t, runBytes: dirBytes(runDir)}
	defer w.close()

	res := AppendResult{Accepted: t.lastSeq}
	var applied []Record
	for i := range records {
		rec := records[i]
		switch {
		case rec.Sequence <= w.t.lastSeq:
			res.Duplicates++

			continue
		case rec.Sequence != w.t.lastSeq+1:
			return res, applied, fmt.Errorf(
				"%w: expected %d, got %d",
				ErrSequenceGap,
				w.t.lastSeq+1,
				rec.Sequence,
			)
		}
		stored, writeErr := w.write(rec)
		if writeErr != nil {
			return res, applied, writeErr
		}
		res.Accepted, res.Written = stored.Sequence, res.Written+1
		if stored.Elided {
			res.Elided++
		}
		applied = append(applied, stored)
	}
	s.rememberTail(dir, w.t)

	return res, applied, nil
}

// batchWriter appends encoded lines to the producer's current segment.
type batchWriter struct {
	dir      string
	t        tail
	runBytes int64
	f        *os.File
}

func (w *batchWriter) write(rec Record) (Record, error) {
	line, stored, err := w.encode(rec)
	if err != nil {
		return Record{}, err
	}
	if err = w.openFor(stored.Sequence, int64(len(line))); err != nil {
		return Record{}, err
	}
	if _, err = w.f.Write(line); err != nil {
		return Record{}, fmt.Errorf("write record: %w", err)
	}
	w.t.size += int64(len(line))
	w.t.lastSeq = stored.Sequence
	w.runBytes += int64(len(line))

	return stored, nil
}

// encode returns the stored line. Output that would push the run past its
// budget keeps its sequence and byte count but loses its text.
func (w *batchWriter) encode(rec Record) ([]byte, Record, error) {
	if rec.Kind == KindOutput && rec.Bytes == 0 {
		rec.Bytes = len(rec.Text)
	}
	line, err := marshalLine(rec)
	if err != nil {
		return nil, Record{}, err
	}
	if rec.Kind == KindOutput {
		return w.fitOutput(line, rec)
	}
	if len(line) > MaxMetadataBytes {
		return nil, Record{}, fmt.Errorf(
			"%w: metadata record exceeds %d bytes",
			ErrInvalidRecord,
			MaxMetadataBytes,
		)
	}

	return w.checkCapacity(line, rec)
}

func (w *batchWriter) fitOutput(line []byte, rec Record) ([]byte, Record, error) {
	if w.runBytes+int64(len(line)) > MaxRunBytes-metadataReserve {
		rec.Text, rec.Elided = "", true
		var err error
		if line, err = marshalLine(rec); err != nil {
			return nil, Record{}, err
		}
	}

	return w.checkCapacity(line, rec)
}

func (w *batchWriter) checkCapacity(line []byte, rec Record) ([]byte, Record, error) {
	if w.runBytes+int64(len(line)) > MaxRunBytes {
		return nil, Record{}, ErrCapacity
	}

	return line, rec, nil
}

func marshalLine(rec Record) ([]byte, error) {
	line, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("encode record: %w", err)
	}

	return append(line, '\n'), nil
}

// openFor makes sure the open segment can take n more bytes, rolling to a new
// segment named after the record's sequence when it cannot.
func (w *batchWriter) openFor(seq uint64, n int64) error {
	if w.f != nil && w.t.size+n <= MaxSegmentBytes {
		return nil
	}
	if w.f == nil && w.t.name != "" && w.t.size+n <= MaxSegmentBytes {
		return w.open(w.t.name)
	}
	w.close()
	w.t.name, w.t.size = segmentName(seq), 0

	return w.open(w.t.name)
}

func (w *batchWriter) open(name string) error {
	path := filepath.Join(w.dir, name)
	if err := checkRegular(path); err != nil {
		return err
	}
	//nolint:gosec // path is built from validated IDs and a generated segment name
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("open segment: %w", err)
	}
	w.f = f

	return nil
}

func (w *batchWriter) close() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}

// applyToManifest mirrors producer transitions and capture conditions into the
// run manifest so each store can decide completion.
func (s *Store) applyToManifest(runID string, applied []Record) error {
	relevant := false
	for i := range applied {
		if applied[i].Kind == KindProducer || applied[i].Kind == KindCapture || applied[i].Elided {
			relevant = true
		}
	}
	if !relevant {
		return nil
	}

	return s.update(runID, func(m *RunManifest) error {
		for i := range applied {
			m.apply(&applied[i])
		}

		return nil
	})
}

func (m *RunManifest) apply(rec *Record) {
	switch {
	case rec.Elided:
		m.addCondition(ConditionCapacity)
	case rec.Kind == KindCapture:
		m.addCondition(rec.Condition)
	case rec.Kind == KindProducer && rec.ChildProducerID != "":
		m.applyChild(rec)
	case rec.Kind == KindProducer:
		m.applyOwn(rec)
	}
}

// applyOwn applies a producer's report about itself.
func (m *RunManifest) applyOwn(rec *Record) {
	if i := m.producer(rec.ProducerID); i >= 0 && rec.ProducerState != "" {
		m.Producers[i].State = rec.ProducerState
	}
	m.DiscoverySealed = m.DiscoverySealed || rec.DiscoverySealed
	if rec.ProducerState == ProducerUnavailable {
		m.addCondition(ConditionSourceUnavailable)
	}
}

// applyChild registers a reserved child or moves a known child to the state the
// parent reports for it.
func (m *RunManifest) applyChild(rec *Record) {
	switch i := m.producer(rec.ChildProducerID); {
	case i >= 0:
		m.Producers[i].State = rec.ProducerState
	case len(m.Producers) < MaxProducers:
		m.Producers = append(m.Producers, ProducerRef{
			ProducerID: rec.ChildProducerID, ParentProducerID: rec.ProducerID,
			State: rec.ProducerState, Location: rec.Location,
		})
	default:
		m.addCondition(ConditionProducerCap)
	}
	if rec.ProducerState == ProducerUnavailable {
		m.addCondition(ConditionSourceUnavailable)
	}
}

// registerFromFirstRecord lets a mirrored producer introduce itself: only the
// producer's own first record (sequence 1, state running or reserved) may.
func (s *Store) registerFromFirstRecord(rec *Record) error {
	if rec.Kind != KindProducer || rec.Sequence != 1 || rec.ChildProducerID != "" {
		return fmt.Errorf("%w: producer is not registered", ErrNotFound)
	}

	return s.RegisterProducer(rec.RunID, ProducerRef{
		ProducerID: rec.ProducerID, ParentProducerID: rec.ParentProducerID,
		State: ProducerRunning, Location: rec.Location,
	})
}

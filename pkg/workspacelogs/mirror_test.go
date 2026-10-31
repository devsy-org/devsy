package workspacelogs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type sinkFixture struct {
	sink  *Sink
	store *Store
	acks  *AckStore
	runID string
	acc   map[string]uint64
}

func newSinkFixture(t *testing.T) *sinkFixture {
	t.Helper()
	store, _ := newTestStore(t)
	acks, err := NewAckStore(filepath.Join(t.TempDir(), "acks"))
	require.NoError(t, err)
	f := &sinkFixture{store: store, acks: acks, runID: NewID(), acc: map[string]uint64{}}
	f.sink, err = NewSink(SinkOptions{
		Store: store, Acks: acks,
		Spec:       RunSpec{RunID: f.runID, Context: testContext, Command: "up"},
		OnAccepted: func(p string, n uint64) { f.acc[p] = n },
	})
	require.NoError(t, err)

	return f
}

func batchOf(runID, producerID string, recs ...Record) BatchRequest {
	req := BatchRequest{
		SchemaVersion: SchemaVersion, RunID: runID, ProducerID: producerID,
		FirstSequence: recs[0].Sequence, LastSequence: recs[len(recs)-1].Sequence,
	}
	for i := range recs {
		data, _ := json.Marshal(recs[i])
		req.Records = append(req.Records, data)
	}

	return req
}

func TestSinkAcceptsAndDeduplicates(t *testing.T) {
	f := newSinkFixture(t)
	pid := NewID()
	seq := newSeq(f.runID, pid)
	inv := NewID()
	req := batchOf(f.runID, pid,
		seq.invocation(inv, hookCreate, InvocationRunning),
		seq.output(inv, StreamStdout, "hello\n"),
	)

	for range 3 {
		res, err := f.sink.Append(req)
		require.NoError(t, err)
		require.Equal(t, MirrorAccepted, res.Result)
		require.Equal(t, uint64(2), res.Accepted)
	}
	require.Equal(t, "hello\n", texts(readAll(t, f.store, ReadRequest{RunID: f.runID})))
	require.Equal(t, uint64(2), f.acc[pid])
	st, err := f.acks.Load(AckKey{RunID: f.runID, ProducerID: pid, Hop: HopInbound})
	require.NoError(t, err)
	require.Equal(t, uint64(2), st.Acked)
}

func TestSinkRejectsInvalidBatches(t *testing.T) {
	f := newSinkFixture(t)
	pid, inv := NewID(), NewID()
	good := func() []Record {
		seq := newSeq(f.runID, pid)

		return []Record{seq.output(inv, StreamStdout, "x")}
	}
	tooBig := newSeq(f.runID, pid).output(inv, StreamStdout, strings.Repeat("a", MaxChunkBytes+1))
	badStream := newSeq(f.runID, pid).output(inv, Stream("tty"), "x")
	badKind := newSeq(f.runID, pid).rec(Record{Kind: Kind("shell")})
	otherRun := newSeq(NewID(), pid).output(inv, StreamStdout, "x")

	cases := map[string]BatchRequest{
		"wrong run":     batchOf(NewID(), pid, good()...),
		"foreign rec":   batchOf(f.runID, pid, otherRun),
		"bad producer":  batchOf(f.runID, "../etc", good()...),
		"oversized":     batchOf(f.runID, pid, tooBig),
		"wrong stream":  batchOf(f.runID, pid, badStream),
		"wrong kind":    batchOf(f.runID, pid, badKind),
		"old schema":    func() BatchRequest { r := batchOf(f.runID, pid, good()...); r.SchemaVersion = 0; return r }(),
		"range mismtch": func() BatchRequest { r := batchOf(f.runID, pid, good()...); r.LastSequence = 9; return r }(),
		"empty":         {SchemaVersion: SchemaVersion, RunID: f.runID, ProducerID: pid},
		"garbage": {
			SchemaVersion: SchemaVersion,
			RunID:         f.runID,
			ProducerID:    pid,
			FirstSequence: 1,
			LastSequence:  1,
			Records:       [][]byte{[]byte("{")},
		},
	}
	for name, req := range cases {
		res, err := f.sink.Append(req)
		require.NoError(t, err, name)
		require.Equal(t, MirrorRejected, res.Result, name)
	}
	require.Empty(t, f.acc)
}

func TestSinkRejectsOversizedPage(t *testing.T) {
	f := newSinkFixture(t)
	pid, inv := NewID(), NewID()
	seq := newSeq(f.runID, pid)
	var recs []Record
	for range 40 {
		recs = append(recs, seq.output(inv, StreamStdout, strings.Repeat("a", MaxChunkBytes)))
	}
	res, err := f.sink.Append(batchOf(f.runID, pid, recs...))
	require.NoError(t, err)
	require.Equal(t, MirrorRejected, res.Result)
}

func TestSinkDoesNotAcknowledgeAheadOfPersisted(t *testing.T) {
	f := newSinkFixture(t)
	pid, inv := NewID(), NewID()
	seq := newSeq(f.runID, pid)
	first := seq.output(inv, StreamStdout, "a")
	_ = seq.output(inv, StreamStdout, "b")
	third := seq.output(inv, StreamStdout, "c")

	res, err := f.sink.Append(batchOf(f.runID, pid, first))
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Accepted)

	// Sequence 2 is missing: the receiver must keep acknowledging 1.
	res, err = f.sink.Append(batchOf(f.runID, pid, third))
	require.NoError(t, err)
	require.Equal(t, MirrorAccepted, res.Result)
	require.Equal(t, uint64(1), res.Accepted)
	require.Equal(t, uint64(1), f.acc[pid])
	require.Equal(t, "a", texts(readAll(t, f.store, ReadRequest{RunID: f.runID})))
}

func TestSinkRepresentsRetentionGap(t *testing.T) {
	f := newSinkFixture(t)
	pid, inv := NewID(), NewID()
	seq := newSeq(f.runID, pid)
	res, err := f.sink.Append(batchOf(f.runID, pid, seq.output(inv, StreamStdout, "a")))
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Accepted)

	// Fill the run so the store cannot take anything more.
	junk := filepath.Join(f.store.root, runsDirName, f.runID, "junk")
	require.NoError(t, os.WriteFile(junk, make([]byte, MaxRunBytes), 0o600))

	res, err = f.sink.Append(batchOf(f.runID, pid,
		seq.output(inv, StreamStdout, "b"), seq.output(inv, StreamStdout, "c")))
	require.NoError(t, err)
	require.Equal(t, MirrorRetentionGap, res.Result)
	require.Equal(t, uint64(2), res.GapFirst)
	require.Equal(t, uint64(3), res.GapLast)
	require.Equal(t, uint64(3), res.Accepted)

	m, err := f.store.Manifest(f.runID)
	require.NoError(t, err)
	require.Contains(t, m.Conditions, ConditionRetentionGap)
	require.Equal(t, CapturePartial, m.CaptureState)

	// A later batch for the excluded producer is answered as a gap too.
	res, err = f.sink.Append(batchOf(f.runID, pid, seq.output(inv, StreamStdout, "d")))
	require.NoError(t, err)
	require.Equal(t, MirrorRetentionGap, res.Result)
	require.Equal(t, uint64(4), res.GapLast)
	require.Equal(t, "a", texts(readAll(t, f.store, ReadRequest{RunID: f.runID})))
}

func TestSinkRejectsLateProducerAfterSeal(t *testing.T) {
	f := newSinkFixture(t)
	require.NoError(t, f.store.SealDiscovery(f.runID))
	pid := NewID()
	res, err := f.sink.Append(batchOf(f.runID, pid, newSeq(f.runID, pid).terminal(false)))
	require.NoError(t, err)
	require.Equal(t, MirrorRejected, res.Result)
}

func TestAckStorePersistsForwardOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acks")
	acks, err := NewAckStore(dir)
	require.NoError(t, err)
	run, pid := NewID(), NewID()

	require.NoError(t, acks.Advance(AckKey{RunID: run, ProducerID: pid, Hop: HopUpstream}, 7))
	require.NoError(t, acks.Advance(AckKey{RunID: run, ProducerID: pid, Hop: HopUpstream}, 3))
	require.NoError(t, acks.Advance(AckKey{RunID: run, ProducerID: pid, Hop: HopInbound}, 2))

	reopened, err := NewAckStore(dir)
	require.NoError(t, err)
	up, err := reopened.Load(AckKey{RunID: run, ProducerID: pid, Hop: HopUpstream})
	require.NoError(t, err)
	in, err := reopened.Load(AckKey{RunID: run, ProducerID: pid, Hop: HopInbound})
	require.NoError(t, err)
	require.Equal(t, uint64(7), up.Acked)
	require.Equal(t, uint64(2), in.Acked)

	_, err = reopened.Load(AckKey{RunID: "../x", ProducerID: pid, Hop: HopUpstream})
	require.ErrorIs(t, err, ErrInvalidID)
	_, err = NewAckStore("relative")
	require.ErrorIs(t, err, ErrUnsafePath)
}

func TestStoreSourceReadsContiguousBatches(t *testing.T) {
	store, _ := newTestStore(t)
	runID := newRun(t, store, uidA)
	pid := newProducer(t, store, runID)
	other := newProducer(t, store, runID)
	seq, otherSeq, inv := newSeq(runID, pid), newSeq(runID, other), NewID()
	for range 5 {
		mustAppend(t, store, seq.output(inv, StreamStdout, "line\n"))
		mustAppend(t, store, otherSeq.output(inv, StreamStdout, "other\n"))
	}
	src := NewStoreSource(store)

	recs, err := src.ReadAfter(runID, pid, 2, MaxPageBytes)
	require.NoError(t, err)
	require.Len(t, recs, 3)
	require.Equal(t, uint64(3), recs[0].Sequence)

	small, err := src.ReadAfter(runID, pid, 0, 1)
	require.NoError(t, err)
	require.Len(t, small, 1)

	none, err := src.ReadAfter(runID, pid, 5, MaxPageBytes)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestStoreSourceReportsSourceGap(t *testing.T) {
	store, _ := newTestStore(t)
	runID := newRun(t, store, uidA)
	pid := newProducer(t, store, runID)
	seq, inv := newSeq(runID, pid), NewID()
	mustAppend(t, store, seq.output(inv, StreamStdout, "a"))
	dir, err := store.producerDir(runID, pid)
	require.NoError(t, err)
	segs, err := listSegments(dir)
	require.NoError(t, err)
	f, err := os.OpenFile( //nolint:gosec // test writes inside its own temp dir
		filepath.Join(dir, segs[0].name), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("not json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = NewStoreSource(store).ReadAfter(runID, pid, 1, MaxPageBytes)
	require.ErrorIs(t, err, ErrSourceGap)
}

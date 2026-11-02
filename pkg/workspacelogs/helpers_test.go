package workspacelogs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testContext = "default"
	hookCreate  = "postCreateCommand"
	uidA        = "uid-a"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
}

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clock := newClock()
	s, err := Open(Options{Root: t.TempDir(), Now: clock.Now})
	require.NoError(t, err)

	return s, clock
}

func newRun(t *testing.T, s *Store, uid string) string {
	t.Helper()
	m, err := s.CreateRun(RunSpec{Context: testContext, WorkspaceUID: uid, Command: "up"})
	require.NoError(t, err)

	return m.RunID
}

func newProducer(t *testing.T, s *Store, runID string) string {
	t.Helper()
	id := NewID()
	require.NoError(
		t,
		s.RegisterProducer(runID, ProducerRef{ProducerID: id, State: ProducerRunning}),
	)

	return id
}

// producerSeq numbers records for one test producer.
type producerSeq struct {
	runID, producerID string
	next              uint64
}

func newSeq(runID, producerID string) *producerSeq {
	return &producerSeq{runID: runID, producerID: producerID, next: 1}
}

func (p *producerSeq) rec(r Record) Record {
	r.SchemaVersion = SchemaVersion
	r.RunID, r.ProducerID = p.runID, p.producerID
	r.Sequence = p.next
	r.Time = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	p.next++

	return r
}

func (p *producerSeq) output(inv string, stream Stream, text string) Record {
	return p.rec(Record{Kind: KindOutput, InvocationID: inv, Stream: stream, Text: text})
}

func (p *producerSeq) invocation(inv, hook string, state InvocationState) Record {
	return p.rec(
		Record{Kind: KindInvocation, InvocationID: inv, Hook: hook, State: state, Phase: "setup"},
	)
}

func (p *producerSeq) terminal(sealed bool) Record {
	return p.rec(
		Record{Kind: KindProducer, ProducerState: ProducerTerminal, DiscoverySealed: sealed},
	)
}

func mustAppend(t *testing.T, s *Store, recs ...Record) AppendResult {
	t.Helper()
	res, err := s.Append(recs)
	require.NoError(t, err)

	return res
}

// finishRun seals discovery and marks one producer terminal so the run is no
// longer active.
func finishRun(t *testing.T, s *Store, runID string) {
	t.Helper()
	pid := newProducer(t, s, runID)
	mustAppend(t, s, newSeq(runID, pid).terminal(true))
}

func readAll(t *testing.T, s *Store, req ReadRequest) []Record {
	t.Helper()
	var all []Record
	for {
		resp, err := s.Read(req)
		require.NoError(t, err)
		all = append(all, resp.Records...)
		if !resp.HasMore {
			return all
		}
		req.After = resp.NextCursor
	}
}

func texts(recs []Record) string {
	var b strings.Builder
	for i := range recs {
		if recs[i].Kind == KindOutput {
			b.WriteString(recs[i].Text)
		}
	}

	return b.String()
}

func readTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // test walks its own temp dir
		b.Write(data)

		return readErr
	}))

	return b.String()
}

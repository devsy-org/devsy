package livetee

import (
	"bytes"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type teeSuite struct{ suite.Suite }

func TestTeeSuite(t *testing.T) { suite.Run(t, new(teeSuite)) }

type blockedWriter struct {
	entered chan struct{}
	release chan struct{}
	writes  atomic.Int64
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	<-w.release
	return len(p), nil
}

func (s *teeSuite) TestBlockedDropsAndReleases() {
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var captured bytes.Buffer
	tee := New(&captured, w)
	_, err := tee.Write([]byte("first"))
	s.Require().NoError(err)
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		s.FailNow("writer not entered")
	}
	payload := bytes.Repeat([]byte("x"), (queueChunks+2)*chunkSize)
	_, err = tee.Write(payload)
	s.Require().NoError(err)
	s.Equal(int64(len("first")+(queueChunks+2)*chunkSize), tee.Close())
	s.Equal(append([]byte("first"), payload...), captured.Bytes())
	close(w.release)
	select {
	case <-tee.done:
		s.Equal(int64(1), w.writes.Load())
	case <-time.After(5 * time.Second):
		s.FailNow("writer did not finish")
	}
}

func (s *teeSuite) TestLiveOrder() {
	var captured, live bytes.Buffer
	tee := New(&captured, &live)
	for _, part := range []string{"one", "two", "three"} {
		_, err := tee.Write([]byte(part))
		s.Require().NoError(err)
	}
	s.Zero(tee.Close())
	s.Equal("onetwothree", captured.String())
	s.Equal(captured.String(), live.String())
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, io.ErrShortWrite }

func (s *teeSuite) TestShortWriteCountsDroppedBytes() {
	var captured bytes.Buffer
	tee := New(&captured, shortWriter{})
	_, err := tee.Write([]byte("123456"))
	s.Require().NoError(err)
	s.Equal(int64(3), tee.Close())
	s.Equal("123456", captured.String())
}

func (s *teeSuite) TestAbandonedCloseRunsOnce() {
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var captured bytes.Buffer
	closed := make(chan struct{})
	var count atomic.Int64
	tee := NewWithClose(&captured, w, func() error { count.Add(1); close(closed); return nil })
	_, err := tee.Write([]byte("test"))
	s.Require().NoError(err)
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		s.FailNow("writer not entered")
	}
	_, abandoned := tee.CloseWithStatus()
	s.True(abandoned)
	s.Zero(count.Load())
	close(w.release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		s.FailNow("writer not closed")
	}
	s.Equal(int64(1), count.Load())
	select {
	case <-tee.done:
	case <-time.After(5 * time.Second):
		s.FailNow("goroutine did not exit")
	}
	s.Equal(int64(4), tee.Close())
	s.Equal(int64(1), count.Load())
}

func (s *teeSuite) TestSharedWriterOrderAndClose() {
	var live bytes.Buffer
	var count atomic.Int64
	tee := NewWithClose(io.Discard, nil, func() error { count.Add(1); return nil })
	out, stderr := tee.Writer(&live), tee.Writer(&live)
	_, err := out.Write([]byte("one"))
	s.Require().NoError(err)
	_, err = stderr.Write([]byte("two"))
	s.Require().NoError(err)
	s.Require().NoError(tee.Finish())
	s.Equal("onetwo", live.String())
	s.Equal(int64(1), count.Load())
	_, err = out.Write([]byte("late"))
	s.Require().NoError(err)
	s.Equal(int64(4), tee.Close())
	s.Equal("onetwo", live.String())
}

func (s *teeSuite) TestCompletedDrainWinsExpiredDeadline() {
	done := make(chan struct{})
	close(done)
	deadline := make(chan time.Time)
	close(deadline)
	tee := &Tee{done: done}
	for range 128 {
		s.False(tee.waitForDrain(deadline))
	}
}

func (s *teeSuite) TestDropsPreserveWriteAndCloseErrors() {
	closeErr := errors.New("close failed")
	tee := NewWithClose(io.Discard, shortWriter{}, func() error { return closeErr })
	_, err := tee.Write([]byte("123456"))
	s.Require().NoError(err)
	err = tee.Finish()
	s.Require().ErrorIs(err, closeErr)
	s.ErrorIs(err, io.ErrShortWrite)
	s.ErrorContains(err, "3 bytes dropped")
}

type fullErrorWriter struct{ err error }

func (w fullErrorWriter) Write(p []byte) (int, error) { return len(p), w.err }

func (s *teeSuite) TestFullWritePreservesError() {
	writeErr := errors.New("live write failed")
	tee := New(io.Discard, fullErrorWriter{err: writeErr})
	_, err := tee.Write([]byte("123456"))
	s.Require().NoError(err)
	s.ErrorIs(tee.Finish(), writeErr)
	s.Zero(tee.Close())
}

func (s *teeSuite) TestBlockedCloseHookIsBounded() {
	entered, release := make(chan struct{}), make(chan struct{})
	tee := NewWithClose(io.Discard, nil, func() error { close(entered); <-release; return nil })
	result := make(chan error, 1)
	go func() { result <- tee.Finish() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		s.FailNow("close hook not entered")
	}
	select {
	case err := <-result:
		s.Require().ErrorContains(err, "0 bytes dropped, drain abandoned: true")
	case <-time.After(DrainTimeout + time.Second):
		s.FailNow("blocked close hook held Finish")
	}
	close(release)
	select {
	case <-tee.done:
	case <-time.After(5 * time.Second):
		s.FailNow("close hook did not finish")
	}
}

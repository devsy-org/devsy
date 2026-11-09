package livetee

import (
	"bytes"
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
	s.Equal(int64((queueChunks+2)*chunkSize), tee.Close())
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

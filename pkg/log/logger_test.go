package log

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestAddSink_ForwardsLogLines(t *testing.T) {
	Init(Config{Verbosity: 2}) // info+

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	Infof("hello %s", "world")
	_ = Sync()

	if got := sink.String(); !strings.Contains(got, "hello world") {
		t.Fatalf("sink did not capture log line; got %q", got)
	}
}

func TestAddSink_RemoveStopsForwarding(t *testing.T) {
	Init(Config{Verbosity: 2})

	var sink bytes.Buffer
	remove := AddSink(&sink)

	Infof("before remove")
	_ = Sync()
	remove()
	Infof("after remove")
	_ = Sync()

	got := sink.String()
	if !strings.Contains(got, "before remove") {
		t.Fatalf("sink missed line written before remove: %q", got)
	}
	if strings.Contains(got, "after remove") {
		t.Fatalf("sink received line after remove: %q", got)
	}
}

func TestAddSink_ConcurrentSinksAreIndependent(t *testing.T) {
	Init(Config{Verbosity: 2})

	var a, b bytes.Buffer
	removeA := AddSink(&a)
	removeB := AddSink(&b)
	defer removeA()
	defer removeB()

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { Infof("line") })
	}
	wg.Wait()
	_ = Sync()

	if !strings.Contains(a.String(), "line") || !strings.Contains(b.String(), "line") {
		t.Fatalf("both sinks should have seen the log line; a=%q b=%q", a.String(), b.String())
	}
}

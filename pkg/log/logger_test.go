package log

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
)

func TestColorEnabledHonorsNoColor(t *testing.T) {
	previous, existed := os.LookupEnv("NO_COLOR")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("NO_COLOR", previous)
		} else {
			_ = os.Unsetenv("NO_COLOR")
		}
	})
	_ = os.Setenv("NO_COLOR", "1")
	if colorEnabled() {
		t.Fatal("colorEnabled returned true with NO_COLOR set")
	}
}

func TestResolveLevelPrecedence(t *testing.T) {
	tests := []struct {
		cfg  Config
		want Level
	}{
		{Config{DefaultLevel: LevelWarnName}, slog.LevelWarn},
		{Config{Level: LevelDebugName, DefaultLevel: LevelWarnName}, slog.LevelDebug},
		{Config{DefaultLevel: "invalid"}, slog.LevelWarn},
	}
	for _, tt := range tests {
		if got := resolveLevel(tt.cfg); got != tt.want {
			t.Fatalf("resolveLevel(%+v) = %v, want %v", tt.cfg, got, tt.want)
		}
	}
}

func TestLevelFromString(t *testing.T) {
	for _, level := range logging.LevelNames() {
		if _, ok := logging.ParseLevel(level); !ok {
			t.Errorf("LevelFromString(%q) rejected valid level", level)
		}
	}
	if _, ok := logging.ParseLevel("verbose"); ok {
		t.Fatal("LevelFromString accepted invalid level")
	}
}

func TestAddSink_ForwardsLogLines(t *testing.T) {
	Init(Config{Level: LevelDebugName})

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
	Init(Config{Level: LevelDebugName})

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
	Init(Config{Level: LevelDebugName})

	// Production callers (the MCP layer's io.Pipe writer) are thread-safe;
	// AddSink doesn't serialize the per-sink Write. Wrap bytes.Buffer for the test.
	a := newSyncBuffer()
	b := newSyncBuffer()
	removeA := AddSink(a)
	removeB := AddSink(b)
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

func TestInitRedactsSecretsBeforeWritingSinks(t *testing.T) {
	Init(
		Config{
			Level:    LevelDebugName,
			Redactor: redact.FromEntries([]string{"TOKEN=DEVSY_SECRET_TEST_846297"}),
		},
	)

	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()

	Infof("token=%s", "DEVSY_SECRET_TEST_846297")
	_ = Sync()

	got := sink.String()
	if strings.Contains(got, "DEVSY_SECRET_TEST_846297") {
		t.Fatalf("secret escaped logger sink: %q", got)
	}
	if !strings.Contains(got, "token=***") {
		t.Fatalf("redacted value missing from logger sink: %q", got)
	}
}

func TestInitDefaultsToEnvironmentRedaction(t *testing.T) {
	t.Setenv("DEVSY_LOG_DEFAULT_SECRET", "default-secret-846297")
	Init(Config{Level: LevelDebugName})

	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()

	Infof("token=%s", "default-secret-846297")
	_ = Sync()
	if got := sink.String(); strings.Contains(got, "default-secret-846297") {
		t.Fatalf("default logger redactor leaked environment secret: %q", got)
	}
}

func TestRedactingWriterRedactsSecretSplitAcrossWrites(t *testing.T) {
	var out syncBuffer
	w := &redactingWriter{
		next:     &out,
		redactor: redact.FromEntries([]string{"TOKEN=DEVSY_LOG_SECRET_846297"}),
	}
	_, _ = w.Write([]byte("token=DEVSY_LOG_"))
	_, _ = w.Write([]byte("SECRET_846297"))
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := out.String(); strings.Contains(got, "DEVSY_LOG_SECRET_846297") {
		t.Fatalf("split secret escaped logger sink: %q", got)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSuppressConsoleOutput(t *testing.T) {
	previous := current.Load()
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(
		func() { os.Stderr = oldStderr; current.Store(previous); _ = r.Close(); _ = w.Close() },
	)
	Init(Config{})
	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()
	Error("before suppression")
	restore := SuppressConsoleOutput()
	Error("during suppression")
	restore()
	restore()
	Error("after suppression")
	_ = Sync()
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "before suppression") ||
		strings.Contains(got, "during suppression") ||
		!strings.Contains(got, "after suppression") {
		t.Fatalf("unexpected console output %q", got)
	}
	if !strings.Contains(sink.String(), "during suppression") {
		t.Fatal("suppressed sink output")
	}
}

type sliceSink []*syncBuffer

func (s sliceSink) Write(p []byte) (int, error) { return s[0].Write(p) }

func TestAddSinkRemovesNonComparableRegistrationsIndependently(t *testing.T) {
	var output syncBuffer
	sink := sliceSink{&output}
	removeFirst := AddSink(sink)
	removeSecond := AddSink(sink)
	defer removeSecond()
	removeFirst()
	removeFirst()
	if _, err := extraSinks.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "one" {
		t.Fatalf("got %q", got)
	}
	removeSecond()
	if _, err := extraSinks.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "one" {
		t.Fatalf("got %q", got)
	}
}

func TestMigrationKeepsOversizeStructuredEnvelope(t *testing.T) {
	const registered = "synthetic-private-oversize-98970"
	Init(
		Config{
			Verbosity: 2,
			Format:    "json",
			Redactor:  redact.FromEntries([]string{"TOKEN=" + registered}),
		},
	)
	restore := SuppressConsoleOutput()
	defer restore()
	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()

	Info(strings.Repeat("x", 17*1024) + registered)
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	got := sink.String()
	if strings.Contains(got, registered) {
		t.Fatal("registered fixture escaped masking")
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(got), &record); err != nil {
		t.Fatalf("sink lost JSON object envelope: %.30q: %v", got, err)
	}
	if record["msg"] != strings.Repeat("x", 17*1024)+"***" {
		t.Fatal("oversized message did not retain its safe prefix and mask the registered value")
	}
}

package log

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
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
		name string
		cfg  Config
		want string
	}{
		{name: "persisted default", cfg: Config{DefaultLevel: LevelWarnName}, want: LevelWarnName},
		{
			name: "explicit log level",
			cfg:  Config{Level: LevelDebugName, DefaultLevel: LevelWarnName}, want: LevelDebugName,
		},
		{
			name: "verbosity beats log level",
			cfg: Config{
				Verbosity:    1,
				VerbositySet: true,
				Level:        LevelDebugName,
			},
			want: LevelInfoName,
		},
		{
			name: "debug beats verbosity",
			cfg:  Config{Verbosity: 1, VerbositySet: true, Debug: true}, want: LevelDebugName,
		},
		{
			name: "quiet beats debug",
			cfg:  Config{Debug: true, Quiet: true}, want: LevelErrorName,
		},
		{
			name: "log level beats persisted default",
			cfg:  Config{Level: LevelWarnName, DefaultLevel: LevelInfoName}, want: LevelWarnName,
		},
		{
			name: "explicit verbosity beats log level",
			cfg: Config{
				Verbosity:    1,
				VerbositySet: true,
				Level:        LevelErrorName,
			},
			want: LevelInfoName,
		},
		{
			name: "debug beats log level",
			cfg:  Config{Debug: true, Level: LevelErrorName}, want: LevelDebugName,
		},
		{
			name: "quiet beats log level",
			cfg:  Config{Quiet: true, Level: LevelTraceName}, want: LevelErrorName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLevel(tt.cfg).String(); got != tt.want {
				t.Fatalf("resolveLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLevelExplicitFalseValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "explicit debug=false falls through to log level",
			cfg: Config{
				Debug:        false,
				Level:        LevelDebugName,
				DefaultLevel: LevelWarnName,
			},
			want: LevelDebugName,
		},
		{
			name: "explicit quiet=false falls through to log level",
			cfg: Config{
				Quiet:        false,
				Level:        LevelInfoName,
				DefaultLevel: LevelWarnName,
			},
			want: LevelInfoName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLevel(tt.cfg).String(); got != tt.want {
				t.Fatalf("resolveLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLevelDefaultsToWarn(t *testing.T) {
	if got := resolveLevel(Config{DefaultLevel: DefaultLevel}).String(); got != LevelWarnName {
		t.Fatalf("resolveLevel() = %q, want %q", got, LevelWarnName)
	}
}

func TestResolveLevelInvalidPersistedDefaultFallsBackToWarn(t *testing.T) {
	if got := resolveLevel(Config{DefaultLevel: "verbose"}).String(); got != LevelWarnName {
		t.Fatalf("resolveLevel() = %q, want %q", got, LevelWarnName)
	}
}

func TestQuietKeepsErrorsVisible(t *testing.T) {
	Init(Config{Quiet: true})
	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()

	Warn("hidden warning")
	Error("visible error")
	_ = Sync()

	if got := sink.String(); strings.Contains(got, "hidden warning") {
		t.Fatalf("quiet logger emitted warning: %q", got)
	}
	if got := sink.String(); !strings.Contains(got, "visible error") {
		t.Fatalf("quiet logger hid error: %q", got)
	}
}

func TestLevelFromString(t *testing.T) {
	for _, level := range ValidLevels() {
		if _, ok := LevelFromString(level); !ok {
			t.Errorf("LevelFromString(%q) rejected valid level", level)
		}
	}
	if _, ok := LevelFromString("verbose"); ok {
		t.Fatal("LevelFromString accepted invalid level")
	}
}

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
			Verbosity: 2,
			Redactor:  secrets.NewRedactor([]string{"TOKEN=DEVSY_SECRET_TEST_846297"}),
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
	Init(Config{Verbosity: 2})

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
		redactor: secrets.NewRedactor([]string{"TOKEN=DEVSY_LOG_SECRET_846297"}),
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

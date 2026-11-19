package log

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
)

// StreamerOptions configures a JSON-aware subprocess log streamer.
type StreamerOptions struct {
	// Redactor is applied before subprocess lines are logged or retained.
	// When omitted, sensitive values from the current environment are masked.
	Redactor *secrets.Redactor
	// FallbackLevel is used for non-structured subprocess output.
	FallbackLevel Level
	// CaptureLines retains this many raw lines for ErrorOutput.
	CaptureLines int
	// CaptureBytes bounds the total raw bytes retained for ErrorOutput,
	// including newline separators. A zero value means no byte limit.
	CaptureBytes int
	// DetectLevelPrefixes preserves a level from timestamp-prefixed plain text.
	DetectLevelPrefixes bool
	// TreatUnknownJSONAsDebug preserves the historical tunnel behavior for
	// JSON messages whose level is absent or unrecognized.
	TreatUnknownJSONAsDebug bool
	DefaultAttrs            []slog.Attr
}

// JSONLogStreamer consumes line-oriented subprocess output. Devsy structured
// log envelopes are decoded and emitted at their original level; all other
// lines are emitted at FallbackLevel.
type JSONLogStreamer struct {
	pw   *io.PipeWriter
	done chan struct{}

	fallbackLevel           Level
	detectLevelPrefixes     bool
	treatUnknownJSONAsDebug bool
	captureLines            int
	captureBytes            int
	redactor                *secrets.Redactor
	defaultAttrs            []slog.Attr
	errorCode               string

	mu            sync.Mutex
	lastLines     []string
	capturedBytes int
	closeOnce     sync.Once
	closeErr      error
}

// NewJSONLogStreamer returns a writer that decodes Devsy JSON log lines while
// preserving ordinary subprocess output at the configured fallback level.
func NewJSONLogStreamer(options StreamerOptions) *JSONLogStreamer {
	pr, pw := io.Pipe()
	streamer := &JSONLogStreamer{
		pw:                      pw,
		done:                    make(chan struct{}),
		fallbackLevel:           options.FallbackLevel,
		detectLevelPrefixes:     options.DetectLevelPrefixes,
		treatUnknownJSONAsDebug: options.TreatUnknownJSONAsDebug,
		captureLines:            options.CaptureLines,
		captureBytes:            options.CaptureBytes,
		redactor:                options.Redactor,
		defaultAttrs:            append([]slog.Attr(nil), options.DefaultAttrs...),
	}
	streamer.redactor = secrets.Combine(
		streamer.redactor,
		secrets.NewEnvironmentRedactor(os.Environ()),
	)
	for i := range streamer.defaultAttrs {
		streamer.defaultAttrs[i] = (&redactHandler{redactor: streamer.redactor}).cleanAttr(
			streamer.defaultAttrs[i],
			true,
		)
	}
	if options.CaptureLines > 0 {
		streamer.lastLines = make([]string, 0, options.CaptureLines)
	}

	go streamer.process(pr)
	return streamer
}

func (s *JSONLogStreamer) Write(p []byte) (int, error) {
	return s.pw.Write(p)
}

// Close stops the reader after draining all complete and unterminated lines.
func (s *JSONLogStreamer) Close() error {
	if s.pw == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.closeErr = s.pw.Close() })
	<-s.done
	return s.closeErr
}

// ErrorOutput returns the most recent captured raw lines, joined by newlines.
func (s *JSONLogStreamer) ErrorOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return strings.Join(s.lastLines, "\n")
}

func (s *JSONLogStreamer) LogLine(line string) {
	line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
	if line == "" {
		return
	}
	s.mu.Lock()
	s.errorCode = ""
	s.mu.Unlock()
	if decoded, ok := decodeJSONLogLine(
		[]byte(line),
	); ok &&
		(decoded.recognized || s.treatUnknownJSONAsDebug) {
		s.emitDecodedLine(decoded)
		return
	}
	line = s.redactLine(line)
	level := s.fallbackLevel
	if s.detectLevelPrefixes {
		if matched, found := extractLevelPrefix(line); matched {
			level = found
		}
	}
	emitRecord(level, time.Time{}, line, s.defaultAttrs)
}

func (s *JSONLogStreamer) ErrorCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorCode
}

func (s *JSONLogStreamer) emitDecodedLine(decoded decodedJSONLogLine) {
	decoded.text = s.redactor.Redact(decoded.text)
	for i := range decoded.attrs {
		decoded.attrs[i] = (&redactHandler{redactor: s.redactor}).cleanAttr(decoded.attrs[i], true)
	}
	if decoded.recognized && decoded.level >= SlogLevelError {
		s.captureErrorCode(decoded.attrs)
	}
	emitRecord(
		decoded.level,
		decoded.timestamp,
		decoded.text,
		mergeAttrs(s.defaultAttrs, decoded.attrs),
	)
}

func (s *JSONLogStreamer) captureErrorCode(attrs []slog.Attr) {
	var code string
	for _, attr := range attrs {
		if attr.Key != "error_code" {
			continue
		}
		switch value := attr.Value.Any().(type) {
		case string:
			code = value
		case json.Number:
			code = value.String()
		case bool:
			if value {
				code = "true"
			} else {
				code = "false"
			}
		}
	}
	if len(code) > 128 {
		code = code[:128]
	}
	s.mu.Lock()
	s.errorCode = code
	s.mu.Unlock()
}

func (s *JSONLogStreamer) redactLine(line string) string {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return s.redactor.Redact(line)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return s.redactor.Redact(line)
	}
	clean := (&redactHandler{redactor: s.redactor}).cleanValue(value)
	if reflect.DeepEqual(value, clean) {
		return line
	}
	if data, err := json.Marshal(clean); err == nil {
		return string(data)
	}
	return s.redactor.Redact(line)
}

const maxStructuredLine = 1024 * 1024

func (s *JSONLogStreamer) process(reader io.Reader) {
	defer close(s.done)
	if closer, ok := reader.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	input := bufio.NewReaderSize(reader, 64*1024)
	var line []byte
	oversized := false
	for {
		chunk, err := input.ReadSlice('\n')
		complete := err == nil
		if complete {
			chunk = chunk[:len(chunk)-1]
		}
		line, oversized = appendStructuredChunk(line, chunk, oversized)
		end := err != nil && err != bufio.ErrBufferFull
		if complete || end {
			s.processLine(line, oversized)
			line, oversized = nil, false
		}
		if end {
			logStreamReadError(err)
			return
		}
	}
}

func appendStructuredChunk(line, chunk []byte, oversized bool) ([]byte, bool) {
	if oversized {
		return line, true
	}
	if len(line)+len(chunk) <= maxStructuredLine {
		return append(line, chunk...), false
	}
	return nil, true
}

func (s *JSONLogStreamer) processLine(line []byte, oversized bool) {
	if oversized {
		s.mu.Lock()
		s.errorCode = ""
		s.mu.Unlock()
		message := "[oversized subprocess output truncated]"
		emitRecord(s.fallbackLevel, time.Time{}, message, s.defaultAttrs)
		s.capture(message)
	} else if len(line) > 0 {
		text := strings.TrimSuffix(string(line), "\r")
		s.LogLine(text)
		s.capture(s.redactLine(text))
	}
}

func logStreamReadError(err error) {
	if err != io.EOF {
		Debugf("error reading subprocess output: %v", err)
	}
}

func (s *JSONLogStreamer) capture(line string) {
	if s.captureLines <= 0 && s.captureBytes <= 0 {
		return
	}
	if s.captureBytes > 0 && len(line) > s.captureBytes {
		line = line[len(line)-s.captureBytes:]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.overCaptureLimit(len(line)) {
		s.dropOldestLine()
	}
	if s.byteOverflow(len(line)) {
		line = line[len(line)-(s.captureBytes-s.capturedBytes-1):]
	}
	if len(s.lastLines) > 0 {
		s.capturedBytes++
	}
	s.lastLines = append(s.lastLines, line)
	s.capturedBytes += len(line)
}

// overCaptureLimit reports whether the oldest line must go before a new line of
// size n can be captured.
func (s *JSONLogStreamer) overCaptureLimit(n int) bool {
	if s.captureLines > 0 && len(s.lastLines) >= s.captureLines {
		return true
	}
	return s.captureBytes > 0 && len(s.lastLines) > 0 && s.capturedBytes+1+n > s.captureBytes
}

// byteOverflow reports whether a new line of size n must be cut to fit the
// byte budget, after older lines were dropped.
func (s *JSONLogStreamer) byteOverflow(n int) bool {
	return s.captureBytes > 0 && len(s.lastLines) > 0 && s.capturedBytes+1+n > s.captureBytes
}

func (s *JSONLogStreamer) dropOldestLine() {
	if len(s.lastLines) == 0 {
		return
	}
	s.capturedBytes -= len(s.lastLines[0])
	if len(s.lastLines) > 1 {
		s.capturedBytes--
	}
	s.lastLines = s.lastLines[1:]
}

type decodedJSONLogLine struct {
	text       string
	level      Level
	recognized bool
	timestamp  time.Time
	attrs      []slog.Attr
}

func decodeJSONLogLine(line []byte) (decodedJSONLogLine, bool) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var obj map[string]any
	if decoder.Decode(&obj) != nil || obj == nil {
		return decodedJSONLogLine{}, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return decodedJSONLogLine{}, false
	}
	text, ok := obj[slog.MessageKey].(string)
	if !ok || text == "" {
		text, _ = obj["message"].(string)
	}
	if text == "" {
		return decodedJSONLogLine{}, false
	}
	rawLevel, _ := obj["level"].(string)
	level, recognized := normalizeLevel(rawLevel)
	var timestamp time.Time
	for _, key := range []string{"ts", slog.TimeKey} {
		rawTime, _ := obj[key].(string)
		var err error
		timestamp, err = time.Parse(time.RFC3339Nano, rawTime)
		if err != nil {
			timestamp, err = time.Parse("2006-01-02T15:04:05.000Z0700", rawTime)
		}
		if err == nil {
			break
		}
	}
	delete(obj, slog.MessageKey)
	delete(obj, "message")
	delete(obj, "level")
	delete(obj, "ts")
	delete(obj, slog.TimeKey)
	attrs := make([]slog.Attr, 0, len(obj))
	for k, v := range obj {
		attrs = append(attrs, slog.Any(k, v))
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
	return decodedJSONLogLine{
		text:       text,
		level:      level,
		recognized: recognized,
		timestamp:  timestamp,
		attrs:      attrs,
	}, true
}

func normalizeLevel(raw string) (Level, bool) {
	switch strings.ToLower(raw) {
	case "trace":
		return SlogLevelTrace, true
	case "debug":
		return SlogLevelDebug, true
	case "info":
		return SlogLevelInfo, true
	case "warning", "warn":
		return SlogLevelWarn, true
	case "error", "panic":
		return SlogLevelError, true
	case "fatal":
		return SlogLevelFatal, true
	default:
		return SlogLevelDebug, false
	}
}

func extractLevelPrefix(line string) (bool, Level) {
	parts := strings.Fields(line)
	if len(parts) < 2 || !strings.Contains(parts[0], ":") {
		return false, 0
	}
	level, ok := normalizeLevel(parts[1])
	return ok, level
}

func mergeAttrs(defaults, explicit []slog.Attr) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(defaults)+len(explicit))
	for _, attr := range defaults {
		if isEnvelopeKey(attr.Key) || containsAttr(explicit, attr.Key) ||
			containsAttr(attrs, attr.Key) {
			continue
		}
		attrs = append(attrs, attr)
	}
	return append(attrs, explicit...)
}

func containsAttr(attrs []slog.Attr, key string) bool {
	for _, attr := range attrs {
		if attr.Key == key {
			return true
		}
	}
	return false
}

package log

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"go.uber.org/zap/zapcore"
)

// StreamerOptions configures a JSON-aware subprocess log streamer.
type StreamerOptions struct {
	// FallbackLevel is used for non-structured subprocess output.
	FallbackLevel int
	// CaptureLines retains this many raw lines for ErrorOutput.
	CaptureLines int
	// DetectLevelPrefixes preserves a level from timestamp-prefixed plain text.
	DetectLevelPrefixes bool
	// TreatUnknownJSONAsDebug preserves the historical tunnel behavior for
	// JSON messages whose level is absent or unrecognized.
	TreatUnknownJSONAsDebug bool
}

// JSONLogStreamer consumes line-oriented subprocess output. Devsy structured
// log envelopes are decoded and emitted at their original level; all other
// lines are emitted at FallbackLevel.
type JSONLogStreamer struct {
	pw   *io.PipeWriter
	done chan struct{}

	fallbackLevel           zapcore.Level
	detectLevelPrefixes     bool
	treatUnknownJSONAsDebug bool
	captureLines            int

	mu        sync.Mutex
	lastLines []string
	closeOnce sync.Once
	closeErr  error
}

// NewJSONLogStreamer returns a writer that decodes Devsy JSON log lines while
// preserving ordinary subprocess output at the configured fallback level.
func NewJSONLogStreamer(options StreamerOptions) *JSONLogStreamer {
	pr, pw := io.Pipe()
	streamer := &JSONLogStreamer{
		pw:                      pw,
		done:                    make(chan struct{}),
		fallbackLevel:           verbosityConstToZapLevel(options.FallbackLevel),
		detectLevelPrefixes:     options.DetectLevelPrefixes,
		treatUnknownJSONAsDebug: options.TreatUnknownJSONAsDebug,
		captureLines:            options.CaptureLines,
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

// LogLine processes one line. It is exported for callers that need to feed a
// line directly, while Write remains the normal subprocess boundary API.
func (s *JSONLogStreamer) LogLine(line string) {
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "\r", "")
	if line == "" {
		return
	}

	if decoded, ok := decodeJSONLogLine([]byte(line)); ok &&
		(decoded.recognized || s.treatUnknownJSONAsDebug) {
		logAtZapLevel(decoded.level, decoded.text)
	} else if s.detectLevelPrefixes {
		if matched, level := extractLevelPrefix(line); matched {
			logAtZapLevel(level, line)
		} else {
			logAtZapLevel(s.fallbackLevel, line)
		}
	} else {
		logAtZapLevel(s.fallbackLevel, line)
	}
}

func (s *JSONLogStreamer) process(reader io.Reader) {
	defer close(s.done)
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		s.LogLine(line)
		s.capture(line)
	}
	if err := scanner.Err(); err != nil {
		Debugf("error reading subprocess output: %v", err)
	}
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (s *JSONLogStreamer) capture(line string) {
	if s.captureLines <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lastLines) >= s.captureLines {
		s.lastLines = s.lastLines[1:]
	}
	s.lastLines = append(s.lastLines, line)
}

type jsonLine struct {
	Message string `json:"message,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Level   string `json:"level,omitempty"`
}

func (l *jsonLine) text() string {
	if l.Message != "" {
		return l.Message
	}
	return l.Msg
}

type decodedJSONLogLine struct {
	text       string
	level      zapcore.Level
	recognized bool
}

func decodeJSONLogLine(line []byte) (decodedJSONLogLine, bool) {
	var obj jsonLine
	if err := json.Unmarshal(line, &obj); err != nil || obj.text() == "" {
		return decodedJSONLogLine{}, false
	}
	level, recognized := normalizeZapLevel(obj.Level)
	return decodedJSONLogLine{
		text:       obj.text(),
		level:      level,
		recognized: recognized,
	}, true
}

const infoLevelName = "info"

func normalizeZapLevel(raw string) (zapcore.Level, bool) {
	switch strings.ToLower(raw) {
	case "trace", "debug":
		return zapcore.DebugLevel, true
	case infoLevelName:
		return zapcore.InfoLevel, true
	case "warning", "warn":
		return zapcore.WarnLevel, true
	case "error", "panic", "fatal":
		return zapcore.ErrorLevel, true
	default:
		return zapcore.DebugLevel, false
	}
}

func extractLevelPrefix(line string) (bool, zapcore.Level) {
	parts := strings.Fields(line)
	if len(parts) < 2 || !strings.Contains(parts[0], ":") {
		return false, 0
	}

	switch strings.ToLower(parts[1]) {
	case "trace", "debug":
		return true, zapcore.DebugLevel
	case infoLevelName:
		return true, zapcore.InfoLevel
	case "warning", "warn":
		return true, zapcore.WarnLevel
	case "error", "panic", "fatal":
		return true, zapcore.ErrorLevel
	default:
		return false, 0
	}
}

func logAtZapLevel(level zapcore.Level, message string) {
	switch level {
	case zapcore.DebugLevel:
		Debug(message)
	case zapcore.InfoLevel:
		Info(message)
	case zapcore.WarnLevel:
		Warn(message)
	case zapcore.ErrorLevel:
		Error(message)
	default:
		Debug(message)
	}
}

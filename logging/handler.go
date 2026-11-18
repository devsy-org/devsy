package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
)

type TextStyle int

const (
	TextLowercase TextStyle = iota
	TextUppercase
	TextColored
)

type ReservedKeys int

const (
	DropReserved ReservedKeys = iota
	EscapeReserved
)

type HandlerOptions struct {
	Level        slog.Level
	Format       Format
	TextStyle    TextStyle
	Redactor     *redact.Redactor
	ReservedKeys ReservedKeys
	TrustedAttrs []slog.Attr
}

const timestampLayout = "2006-01-02T15:04:05.000Z0700"

type sanitizer struct {
	redactor *redact.Redactor
	policy   ReservedKeys
	reserved map[string]bool
}

type handler struct {
	sanitizer
	inner   slog.Handler
	grouped bool
	blocked bool
}

func NewHandler(w io.Writer, options HandlerOptions) slog.Handler {
	style := options.TextStyle
	if options.Format == FormatJSON || options.Format == FormatLogfmt {
		style = TextLowercase
	}
	if style == TextColored {
		w = &colorWriter{writer: w}
	}
	opts := &slog.HandlerOptions{
		Level: options.Level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			return replaceEnvelopeAttr(groups, attr, style)
		},
	}
	var inner slog.Handler
	if options.Format == FormatJSON {
		inner = slog.NewJSONHandler(w, opts)
	} else {
		inner = slog.NewTextHandler(w, opts)
	}
	return ProtectHandler(inner, options.Redactor, options.ReservedKeys, options.TrustedAttrs...)
}

func replaceEnvelopeAttr(groups []string, attr slog.Attr, style TextStyle) slog.Attr {
	if len(groups) != 0 {
		return attr
	}
	switch attr.Key {
	case slog.TimeKey:
		return replaceTimeAttr(attr)
	case slog.LevelKey:
		return replaceLevelAttr(attr, style)
	default:
		return attr
	}
}

func replaceTimeAttr(attr slog.Attr) slog.Attr {
	if attr.Value.Kind() == slog.KindTime {
		return slog.String("ts", attr.Value.Time().Format(timestampLayout))
	}
	return attr
}

func replaceLevelAttr(attr slog.Attr, style TextStyle) slog.Attr {
	level, ok := attr.Value.Any().(slog.Level)
	if !ok {
		return attr
	}
	name := LevelName(level)
	if style != TextLowercase {
		name = strings.ToUpper(name)
	}

	return slog.String("level", name)
}

type colorWriter struct {
	writer io.Writer
}

func (w *colorWriter) Write(p []byte) (int, error) {
	token, ok := canonicalLevelToken(p)
	if !ok {
		return w.writer.Write(p)
	}
	name := string(p[token.start:token.end])
	colored := colorLevel(token.level, name)
	output := make([]byte, 0, len(p)+len(colored)-len(name))
	output = append(output, p[:token.start]...)
	output = append(output, colored...)
	output = append(output, p[token.end:]...)
	n, err := w.writer.Write(output)
	return originalWriteCount(n, token.start, token.end, len(p)), err
}

type levelToken struct {
	start int
	end   int
	level slog.Level
}

func canonicalLevelToken(p []byte) (levelToken, bool) {
	start := 0
	if bytes.HasPrefix(p, []byte("ts=")) {
		space := bytes.IndexByte(p, ' ')
		if space < 0 {
			return levelToken{}, false
		}
		start = space + 1
	}
	if !bytes.HasPrefix(p[start:], []byte("level=")) {
		return levelToken{}, false
	}
	start += len("level=")
	end := len(p)
	if offset := bytes.IndexAny(p[start:], " \n"); offset >= 0 {
		end = start + offset
	}
	level, ok := canonicalColorLevel(string(p[start:end]))
	return levelToken{start: start, end: end, level: level}, ok
}

func canonicalColorLevel(name string) (slog.Level, bool) {
	switch name {
	case "TRACE", "DEBUG", "INFO":
		return slog.LevelInfo, true
	case "WARN":
		return slog.LevelWarn, true
	case "ERROR", "FATAL":
		return slog.LevelError, true
	default:
		return 0, false
	}
}

func originalWriteCount(n, start, end, inputLen int) int {
	const prefix = len("\x1b[36m")
	const suffix = len("\x1b[0m")
	switch {
	case n <= start:
		return n
	case n <= start+prefix:
		return start
	case n < end+prefix:
		return n - prefix
	case n <= end+prefix+suffix:
		return end
	case n >= inputLen+prefix+suffix:
		return inputLen
	default:
		return n - prefix - suffix
	}
}

func newSanitizer(
	redactor *redact.Redactor,
	policy ReservedKeys,
	trustedAttrs []slog.Attr,
) sanitizer {
	if redactor == nil {
		redactor = redact.New(redact.Options{})
	}
	reserved := map[string]bool{
		"ts":      true,
		"time":    true,
		"level":   true,
		"msg":     true,
		"message": true,
	}
	reserveTrustedKeys(reserved, trustedAttrs)
	return sanitizer{redactor: redactor, policy: policy, reserved: reserved}
}

func reserveTrustedKeys(reserved map[string]bool, attrs []slog.Attr) {
	for _, attr := range attrs {
		if attr.Key != "" {
			reserved[attr.Key] = true
		} else if attr.Value.Kind() == slog.KindGroup {
			reserveTrustedKeys(reserved, attr.Value.Group())
		}
	}
}

func resolveTrustedAttrs(attrs []slog.Attr) []slog.Attr {
	resolved := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		if attr.Value.Kind() == slog.KindGroup {
			attr.Value = slog.GroupValue(resolveTrustedAttrs(attr.Value.Group())...)
		}
		resolved = append(resolved, attr)
	}
	return resolved
}

func SanitizeAttrs(redactor *redact.Redactor, attrs []slog.Attr) []slog.Attr {
	cleaner := newSanitizer(redactor, DropReserved, nil)
	return cleaner.cleanAttrs(attrs, true)
}

func ProtectHandler(
	next slog.Handler,
	redactor *redact.Redactor,
	policy ReservedKeys,
	trustedAttrs ...slog.Attr,
) slog.Handler {
	trustedAttrs = resolveTrustedAttrs(trustedAttrs)
	h := &handler{inner: next, sanitizer: newSanitizer(redactor, policy, trustedAttrs)}
	if len(trustedAttrs) != 0 {
		cleaned := make([]slog.Attr, 0, len(trustedAttrs))
		for _, attr := range trustedAttrs {
			cleaned = append(cleaned, h.cleanTrusted(attr))
		}
		h.inner = next.WithAttrs(cleaned)
	}
	return h
}

func colorLevel(level slog.Level, name string) string {
	color := "36"
	switch {
	case level >= slog.LevelError:
		color = "31"
	case level >= slog.LevelWarn:
		color = "33"
	}
	return "\x1b[" + color + "m" + name + "\x1b[0m"
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.blocked {
		return h
	}
	cleaned := h.cleanAttrs(attrs, !h.grouped)
	copyHandler := *h
	copyHandler.inner = h.inner.WithAttrs(cleaned)
	return &copyHandler
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" || h.blocked {
		return h
	}
	group, ok := h.clean(slog.Attr{Key: name, Value: slog.GroupValue()}, !h.grouped)
	copyHandler := *h
	if !ok {
		copyHandler.blocked = true
	} else if group.Key != "" {
		copyHandler.inner = h.inner.WithGroup(group.Key)
		copyHandler.grouped = true
	}
	return &copyHandler
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	out := slog.NewRecord(record.Time, record.Level, h.redactor.Redact(record.Message), record.PC)
	if !h.blocked {
		record.Attrs(func(attr slog.Attr) bool {
			if cleaned, ok := h.clean(attr, !h.grouped); ok {
				out.AddAttrs(cleaned)
			}
			return true
		})
	}
	return h.inner.Handle(ctx, out)
}

func (h *sanitizer) cleanTrusted(attr slog.Attr) slog.Attr {
	if attr.Key == "" && attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		cleaned := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			cleaned = append(cleaned, h.cleanTrusted(child))
		}
		return slog.Attr{Value: slog.GroupValue(cleaned...)}
	}
	key := attr.Key
	cleaned, _ := h.clean(attr, false)
	cleaned.Key = key
	return cleaned
}

func (h *sanitizer) cleanAttrs(attrs []slog.Attr, root bool) []slog.Attr {
	cleaned := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if value, ok := h.clean(attr, root); ok {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func (h *sanitizer) clean(attr slog.Attr, root bool) (slog.Attr, bool) {
	key := h.redactor.Redact(attr.Key)
	if root && key != "" {
		var ok bool
		key, ok = h.rootKey(attr.Key, key)
		if !ok {
			return slog.Attr{}, false
		}
	}
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		cleaned := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			if value, ok := h.clean(child, root && key == ""); ok {
				cleaned = append(cleaned, value)
			}
		}
		return slog.Attr{Key: key, Value: slog.GroupValue(cleaned...)}, true
	}
	return slog.Attr{Key: key, Value: h.cleanValue(attr.Key, attr.Value)}, true
}

func (h *sanitizer) rootKey(original, key string) (string, bool) {
	count := h.reservedAncestors(key)
	if !h.isReservedRoot(original, key, count) {
		return key, true
	}
	if h.policy != EscapeReserved {
		return "", false
	}
	if count == 0 {
		count = 1
	}
	for range count {
		key = "user_" + key
	}
	for h.reserved[key] {
		key = "user_" + key
	}
	return key, true
}

func (h *sanitizer) isReservedRoot(original, key string, count int) bool {
	return h.reserved[key] || h.reserved[original] || (h.policy == EscapeReserved && count > 0)
}

func (h *sanitizer) reservedAncestors(key string) int {
	count := 0
	for {
		if h.reserved[key] {
			count++
		}
		if !strings.HasPrefix(key, "user_") {
			return count
		}
		key = strings.TrimPrefix(key, "user_")
	}
}

func (h *sanitizer) cleanValue(key string, value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindString:
		return slog.StringValue(h.redactor.RedactField(key, value.String()))
	case slog.KindAny:
		return slog.AnyValue(h.cleanAny(key, value.Any()))
	default:
		return value
	}
}

func (h *sanitizer) cleanAny(key string, value any) (result any) {
	defer func() {
		if recover() != nil {
			result = "[invalid value]"
		}
	}()
	if err, ok := value.(error); ok {
		return h.redactor.RedactField(key, err.Error())
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "[unsupported value]"
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return "[invalid value]"
	}
	return h.cleanJSON(key, decoded)
}

func (h *sanitizer) cleanJSON(key string, value any) any {
	switch v := value.(type) {
	case string:
		return h.redactor.RedactField(key, v)
	case []any:
		for i := range v {
			v[i] = h.cleanJSON(key, v[i])
		}
	case map[string]any:
		out := make(map[string]any, len(v))
		for childKey, item := range v {
			out[h.redactor.Redact(childKey)] = h.cleanJSON(childKey, item)
		}
		return out
	}
	return value
}

func ParseTimestamp(value string) (time.Time, error) {
	if parsed, err := time.Parse(timestampLayout, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

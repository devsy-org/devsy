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
	flatten  bool
}

type handler struct {
	sanitizer
	inner   slog.Handler
	group   string
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
	flatten bool,
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
	return sanitizer{redactor: redactor, policy: policy, reserved: reserved, flatten: flatten}
}

func (h *sanitizer) reserveTrustedKeys(attrs []slog.Attr, group string) {
	for _, attr := range attrs {
		key := attributePath(group, attr.Key)
		if attr.Key != "" {
			h.reserved[key] = true
		}
		if attr.Value.Kind() == slog.KindGroup && (h.flatten || attr.Key == "") {
			h.reserveTrustedKeys(attr.Value.Group(), key)
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
	cleaner := newSanitizer(redactor, DropReserved, false)
	return cleaner.cleanAttrs(attrs, "", true)
}

func ProtectHandler(
	next slog.Handler,
	redactor *redact.Redactor,
	policy ReservedKeys,
	trustedAttrs ...slog.Attr,
) slog.Handler {
	trustedAttrs = resolveTrustedAttrs(trustedAttrs)
	_, flatten := next.(*slog.TextHandler)
	h := &handler{inner: next, sanitizer: newSanitizer(redactor, policy, flatten)}
	if len(trustedAttrs) != 0 {
		cleaned := h.cleanAttrs(trustedAttrs, "", false)
		h.reserveTrustedKeys(trustedAttrs, "")
		h.reserveTrustedKeys(cleaned, "")
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
	cleaned := h.cleanAttrs(attrs, h.group, true)
	copyHandler := *h
	copyHandler.inner = h.inner.WithAttrs(cleaned)
	return &copyHandler
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" || h.blocked {
		return h
	}
	group, ok := h.clean(slog.Attr{Key: name, Value: slog.GroupValue()}, h.group, true)
	copyHandler := *h
	switch {
	case !ok:
		copyHandler.blocked = true
	case group.Value.Kind() != slog.KindGroup:
		copyHandler.inner = h.inner.WithAttrs([]slog.Attr{group})
		copyHandler.blocked = true
	case group.Key != "":
		copyHandler.inner = h.inner.WithGroup(group.Key)
		copyHandler.group = attributePath(h.group, group.Key)
	}
	return &copyHandler
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	out := slog.NewRecord(record.Time, record.Level, h.redactor.Redact(record.Message), record.PC)
	if !h.blocked {
		record.Attrs(func(attr slog.Attr) bool {
			if cleaned, ok := h.clean(attr, h.group, true); ok {
				out.AddAttrs(cleaned)
			}
			return true
		})
	}
	return h.inner.Handle(ctx, out)
}

func (h *sanitizer) cleanAttrs(attrs []slog.Attr, group string, protect bool) []slog.Attr {
	cleaned := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if value, ok := h.clean(attr, group, protect); ok {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func (h *sanitizer) clean(attr slog.Attr, group string, protect bool) (slog.Attr, bool) {
	key := h.redactor.RedactField("", attr.Key)
	if protect && key != "" && (h.flatten || group == "") {
		var ok bool
		key, ok = h.protectedKey(group, attr.Key, key)
		if !ok {
			return slog.Attr{}, false
		}
	}
	if mask, sensitive := h.redactor.MaskCredentialField(attr.Key); sensitive {
		return slog.String(key, mask), true
	}
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		path := attributePath(group, key)
		cleaned := h.cleanAttrs(attr.Value.Group(), path, protect)
		return slog.Attr{Key: key, Value: slog.GroupValue(cleaned...)}, true
	}
	return slog.Attr{Key: key, Value: h.cleanValue(attr.Value)}, true
}

func attributePath(group, key string) string {
	if group == "" || key == "" {
		return group + key
	}
	return group + "." + key
}

const userKeyPrefix = "user_"

func (h *sanitizer) protectedKey(group, original, key string) (string, bool) {
	count := h.reservedAncestors(group, key)
	if !h.reserved[attributePath(group, key)] && !h.reserved[attributePath(group, original)] &&
		(h.policy != EscapeReserved || count == 0) {
		return key, true
	}
	if h.policy != EscapeReserved {
		return "", false
	}
	shift := max(1, count)
	for step := 1; step <= shift; step++ {
		if h.reserved[attributePath(group, strings.Repeat(userKeyPrefix, step)+key)] {
			shift++
		}
	}
	return strings.Repeat(userKeyPrefix, shift) + key, true
}

func (h *sanitizer) reservedAncestors(group, key string) int {
	count := 0
	for {
		if h.reserved[attributePath(group, key)] {
			count++
		}
		if !strings.HasPrefix(key, userKeyPrefix) {
			return count
		}
		key = strings.TrimPrefix(key, userKeyPrefix)
	}
}

func (h *sanitizer) cleanValue(value slog.Value) slog.Value {
	switch value.Kind() {
	case slog.KindString:
		return slog.AnyValue(h.redactor.SanitizeJSON(value.String()))
	case slog.KindAny:
		return slog.AnyValue(h.cleanAny(value.Any()))
	default:
		if h.secretScalar(value) {
			return slog.StringValue(h.redactor.Mask())
		}
		return value
	}
}

func (h *sanitizer) secretScalar(value slog.Value) bool {
	if h.redactor.ContainsValue(value.String()) {
		return true
	}
	data, err := json.Marshal(value.Any())
	return err == nil && h.redactor.ContainsValue(string(data))
}

func (h *sanitizer) cleanAny(value any) (result any) {
	defer func() {
		if recover() != nil {
			result = "[invalid value]"
		}
	}()
	_, marshalsJSON := value.(json.Marshaler)
	if err, ok := value.(error); ok && !marshalsJSON {
		return h.redactor.SanitizeJSON(err.Error())
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
	return h.redactor.SanitizeJSON(decoded)
}

func ParseTimestamp(value string) (time.Time, error) {
	if parsed, err := time.Parse(timestampLayout, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

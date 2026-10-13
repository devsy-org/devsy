package log

import (
	"errors"
	"fmt"

	zapbuffer "go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

var textRecordPool = zapbuffer.NewPool()

// oneLineTextEncoder wraps a console encoder so every encoded entry is exactly
// one physical record: a single terminating LF, with line breaks and terminal
// control characters inside the record escaped as text. Escaping covers the
// whole serialized record (message, fields, stack) because it is applied to
// the inner encoder's output.
//
// ESC is deliberately left untouched: the color level encoder emits ANSI
// sequences and the serialized buffer does not distinguish formatter bytes from
// payload bytes. Payload ANSI injection is therefore out of scope here.
type oneLineTextEncoder struct{ zapcore.Encoder }

func newOneLineTextEncoder(inner zapcore.Encoder) zapcore.Encoder {
	return &oneLineTextEncoder{Encoder: inner}
}

// Clone must wrap a clone of the inner encoder because zap calls it in
// Core.With and the inner encoder holds the accumulated With fields.
func (e *oneLineTextEncoder) Clone() zapcore.Encoder {
	return newOneLineTextEncoder(e.Encoder.Clone())
}

func (e *oneLineTextEncoder) EncodeEntry(
	entry zapcore.Entry,
	fields []zapcore.Field,
) (*zapbuffer.Buffer, error) {
	original, err := e.Encoder.EncodeEntry(entry, fields)
	if err != nil {
		if original != nil {
			original.Free()
		}
		return nil, err
	}
	if original == nil {
		return nil, errors.New("text encoder returned nil buffer")
	}

	body, hasFinalLF := splitEncodedRecord(original.Bytes())
	if hasFinalLF && !needsRecordEscaping(body) {
		return original, nil
	}

	normalized := textRecordPool.Get()
	appendEscapedRecordBody(normalized, body)
	normalized.AppendByte('\n')
	original.Free()
	return normalized, nil
}

// splitEncodedRecord separates the single final LF terminator from the body.
func splitEncodedRecord(b []byte) ([]byte, bool) {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1], true
	}
	return b, false
}

func forbiddenASCII(b byte) bool {
	return (b < 0x20 && b != '\t' && b != 0x1b) || b == 0x7f
}

func asciiRecordEscape(b byte) string {
	switch b {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\v':
		return `\v`
	case '\f':
		return `\f`
	case 0:
		return `\x00`
	}
	if forbiddenASCII(b) {
		return fmt.Sprintf(`\x%02x`, b)
	}
	return ""
}

func unicodeRecordEscape(b []byte) (int, string) {
	if len(b) >= 2 && b[0] == 0xc2 && b[1] == 0x85 {
		return 2, `\u0085`
	}
	if len(b) >= 3 && b[0] == 0xe2 && b[1] == 0x80 {
		switch b[2] {
		case 0xa8:
			return 3, `\u2028`
		case 0xa9:
			return 3, `\u2029`
		}
	}
	return 0, ""
}

func recordEscapeAt(b []byte) (int, string) {
	if value := asciiRecordEscape(b[0]); value != "" {
		return 1, value
	}
	return unicodeRecordEscape(b)
}

func needsRecordEscaping(body []byte) bool {
	for i := range body {
		if forbiddenASCII(body[i]) {
			return true
		}
		if body[i] >= 0xc2 {
			if width, _ := unicodeRecordEscape(body[i:]); width > 0 {
				return true
			}
		}
	}
	return false
}

func appendEscapedRecordBody(dst *zapbuffer.Buffer, body []byte) {
	for i := 0; i < len(body); {
		width, replacement := recordEscapeAt(body[i:])
		if width > 0 {
			dst.AppendString(replacement)
			i += width
			continue
		}
		dst.AppendByte(body[i])
		i++
	}
}

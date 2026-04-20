package log

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

var logfmtPool = buffer.NewPool()

type logfmtEncoder struct {
	buf    *buffer.Buffer
	fields []zapcore.Field
}

func newLogfmtEncoder() zapcore.Encoder {
	return &logfmtEncoder{buf: logfmtPool.Get()}
}

func (e *logfmtEncoder) Clone() zapcore.Encoder {
	clone := &logfmtEncoder{buf: logfmtPool.Get(), fields: make([]zapcore.Field, len(e.fields))}
	copy(clone.fields, e.fields)
	return clone
}

func (e *logfmtEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	buf := logfmtPool.Get()

	// level
	buf.AppendString("level=")
	buf.AppendString(entry.Level.String())

	// timestamp
	buf.AppendString(" ts=")
	buf.AppendString(entry.Time.Format(time.RFC3339))

	// message
	buf.AppendString(" msg=")
	buf.AppendString(quoteLogfmt(entry.Message))

	// caller
	if entry.Caller.Defined {
		buf.AppendString(" caller=")
		buf.AppendString(entry.Caller.TrimmedPath())
	}

	// fields from With() + fields from this entry
	allFields := append(e.fields, fields...)
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range allFields {
		f.AddTo(enc)
	}
	for k, v := range enc.Fields {
		buf.AppendString(" ")
		buf.AppendString(k)
		buf.AppendString("=")
		buf.AppendString(quoteLogfmt(fmt.Sprint(v)))
	}

	buf.AppendString("\n")
	return buf, nil
}

func quoteLogfmt(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\"=") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// Required interface methods — delegate to buffer for simple types.
func (e *logfmtEncoder) AddArray(key string, arr zapcore.ArrayMarshaler) error   { return nil }
func (e *logfmtEncoder) AddObject(key string, obj zapcore.ObjectMarshaler) error { return nil }
func (e *logfmtEncoder) AddBinary(key string, val []byte)                        {}
func (e *logfmtEncoder) AddByteString(key string, val []byte)                    {}
func (e *logfmtEncoder) AddBool(key string, val bool) {
	e.fields = append(e.fields, zapcore.Field{Key: key, Type: zapcore.BoolType, Integer: boolToInt(val)})
}
func (e *logfmtEncoder) AddComplex128(key string, val complex128) {}
func (e *logfmtEncoder) AddComplex64(key string, val complex64)   {}
func (e *logfmtEncoder) AddDuration(key string, val time.Duration) {
	e.fields = append(e.fields, zapcore.Field{Key: key, Type: zapcore.StringType, String: val.String()})
}
func (e *logfmtEncoder) AddFloat64(key string, val float64) {
	e.fields = append(e.fields, zapcore.Field{Key: key, Type: zapcore.Float64Type})
}
func (e *logfmtEncoder) AddFloat32(key string, val float32) {}
func (e *logfmtEncoder) AddInt(key string, val int)         { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddInt64(key string, val int64) {
	e.fields = append(e.fields, zapcore.Field{Key: key, Type: zapcore.Int64Type, Integer: val})
}
func (e *logfmtEncoder) AddInt32(key string, val int32)   { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddInt16(key string, val int16)   { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddInt8(key string, val int8)     { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddString(key, val string) {
	e.fields = append(e.fields, zapcore.Field{Key: key, Type: zapcore.StringType, String: val})
}
func (e *logfmtEncoder) AddTime(key string, val time.Time) {
	e.AddString(key, val.Format(time.RFC3339))
}
func (e *logfmtEncoder) AddUint(key string, val uint)     { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddUint64(key string, val uint64) { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddUint32(key string, val uint32) { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddUint16(key string, val uint16) { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddUint8(key string, val uint8)   { e.AddInt64(key, int64(val)) }
func (e *logfmtEncoder) AddUintptr(key string, val uintptr) {}
func (e *logfmtEncoder) AddReflected(key string, val interface{}) error {
	e.AddString(key, fmt.Sprint(val))
	return nil
}
func (e *logfmtEncoder) OpenNamespace(key string) {}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

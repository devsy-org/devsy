package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
)

const (
	testSecret = "secret-value"
	nestedKey  = "nested"
	testDebug  = "DEBUG"
)

func TestProviderConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, level, format string
		want                ProviderConfig
		invalid             InvalidConfig
	}{
		{"defaults", "", "", ProviderConfig{slog.LevelInfo, FormatJSON}, InvalidConfig{}},
		{"case and plain", testDebug, "PLAIN", ProviderConfig{slog.LevelDebug, FormatText}, InvalidConfig{}},
		{"auto", "INFO", "AUTO", ProviderConfig{slog.LevelInfo, FormatJSON}, InvalidConfig{}},
		{"logfmt", "trace", "LOGFMT", ProviderConfig{LevelTrace, FormatLogfmt}, InvalidConfig{}},
		{"json", "warn", "JSON", ProviderConfig{slog.LevelWarn, FormatJSON}, InvalidConfig{}},
		{"text", "error", "TEXT", ProviderConfig{slog.LevelError, FormatText}, InvalidConfig{}},
		{
			"invalid level", " debug", "logfmt",
			ProviderConfig{slog.LevelInfo, FormatLogfmt},
			InvalidConfig{Level: true},
		},
		{
			"invalid format", testDebug, " text",
			ProviderConfig{slog.LevelDebug, FormatJSON},
			InvalidConfig{Format: true},
		},
		{
			"both invalid", "bad", "bad",
			ProviderConfig{slog.LevelInfo, FormatJSON},
			InvalidConfig{Level: true, Format: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, invalid := ResolveProvider(tc.level, tc.format)
			if config != tc.want || invalid != tc.invalid {
				t.Fatalf("resolution = %+v %+v, want %+v %+v", config, invalid, tc.want, tc.invalid)
			}
		})
	}
}

func TestHandlerRedactionAndReservedFields(t *testing.T) {
	var output bytes.Buffer
	r := redact.New(redact.Options{Values: []string{testSecret}})
	h := NewHandler(
		&output,
		HandlerOptions{
			Format:       FormatJSON,
			Redactor:     r,
			ReservedKeys: EscapeReserved,
			TrustedAttrs: []slog.Attr{slog.String("provider", "trusted")},
		},
	)
	h = h.WithAttrs(
		[]slog.Attr{
			slog.Group("", slog.String(slog.LevelKey, "fake")),
			slog.String("user_level", "second"),
		},
	)
	if output.Len() != 0 {
		t.Fatal("handler wrote before Handle")
	}
	record := slog.NewRecord(
		time.Date(2024, 1, 2, 3, 4, 5, 6000000, time.UTC),
		slog.LevelInfo,
		testSecret,
		0,
	)
	record.AddAttrs(
		slog.String("provider", "forged"),
		slog.Any("payload", map[string]any{testSecret: []any{testSecret}}),
	)
	if err := h.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), testSecret) {
		t.Fatalf("secret leaked: %s", output.String())
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		slog.LevelKey:     "info",
		"user_level":      "fake",
		"user_user_level": "second",
		"provider":        "trusted",
		"user_provider":   "forged",
		"ts":              "2024-01-02T03:04:05.006Z",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}
}

func TestGroupAndTimestamp(t *testing.T) {
	var output bytes.Buffer
	h := NewHandler(&output, HandlerOptions{Format: FormatJSON}).WithGroup(slog.LevelKey)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	r.AddAttrs(slog.String("key", "value"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), `"key"`) {
		t.Fatalf("reserved root group was not dropped: %s", output.String())
	}
	for _, value := range []string{
		"2024-01-02T03:04:05.006Z",
		"2024-01-02T03:04:05.123456789+01:00",
		"2024-01-02T03:04:05.006+0100",
	} {
		if _, err := ParseTimestamp(value); err != nil {
			t.Fatalf("timestamp %q: %v", value, err)
		}
	}
}

func TestBoundAndRecordAttributesShareGroup(t *testing.T) {
	var output bytes.Buffer
	h := NewHandler(&output, HandlerOptions{Format: FormatJSON}).WithGroup("group")
	h = h.WithAttrs([]slog.Attr{slog.String("first", "one")})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("second", "two"))
	if err := h.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Group map[string]string `json:"group"`
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Group["first"] != "one" || got.Group["second"] != "two" {
		t.Fatalf("group lost attributes: %s", output.String())
	}
}

func TestAutoResolutionProducesJSON(t *testing.T) {
	config, invalid := ResolveProvider("", "auto")
	if invalid != (InvalidConfig{}) || config.Format != FormatJSON {
		t.Fatalf("unexpected auto resolution: %+v %+v", config, invalid)
	}
	var output bytes.Buffer
	handler := NewHandler(&output, HandlerOptions{Level: config.Level, Format: config.Format})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || got["msg"] != "message" {
		t.Fatalf("expected JSON record, got %q: %v", output.String(), err)
	}
}

func TestColorLevel(t *testing.T) {
	for _, tc := range []struct {
		level slog.Level
		name  string
		want  string
	}{
		{LevelTrace, "TRACE", "\x1b[36mTRACE\x1b[0m"},
		{slog.LevelDebug, testDebug, "\x1b[36mDEBUG\x1b[0m"},
		{slog.LevelInfo, "INFO", "\x1b[36mINFO\x1b[0m"},
		{slog.LevelWarn, "WARN", "\x1b[33mWARN\x1b[0m"},
		{slog.LevelError, "ERROR", "\x1b[31mERROR\x1b[0m"},
		{LevelFatal, "FATAL", "\x1b[31mFATAL\x1b[0m"},
	} {
		if got := colorLevel(tc.level, tc.name); got != tc.want {
			t.Errorf("level %s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

type countingValue struct {
	calls *int
	value map[string]any
}

func (v countingValue) LogValue() slog.Value {
	*v.calls++
	return slog.AnyValue(v.value)
}

func TestWithAttrsSnapshotsOnceAcrossHandlers(t *testing.T) {
	var output bytes.Buffer
	calls := 0
	value := map[string]any{nestedKey: []any{"initial"}}
	root := NewHandler(&output, HandlerOptions{Format: FormatJSON})
	bound := root.WithAttrs(
		[]slog.Attr{slog.Any("payload", countingValue{calls: &calls, value: value})},
	)
	if calls != 1 || output.Len() != 0 {
		t.Fatalf("binding calls=%d output=%q", calls, output.String())
	}
	value[nestedKey].([]any)[0] = "changed"
	branches := []slog.Handler{bound, bound.WithGroup("group")}
	for _, branch := range branches {
		record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
		if err := branch.Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || strings.Contains(output.String(), "changed") ||
		strings.Count(output.String(), "initial") != 2 {
		t.Fatalf("binding was not a snapshot: calls=%d output=%s", calls, output.String())
	}
}

func TestSanitizeAttrs(t *testing.T) {
	input := []slog.Attr{
		slog.String(slog.LevelKey, "forged"),
		slog.Group("", slog.String("msg", "forged"), slog.String("safe", testSecret)),
		slog.Any("", map[string]any{nestedKey: []any{testSecret, int64(9007199254740993)}}),
	}
	redactor := redact.New(redact.Options{Values: []string{testSecret}})
	cleaned := SanitizeAttrs(redactor, input)
	checkSanitizedAttrs(t, cleaned)
	checkSanitizerInput(t, input)
}

func checkSanitizedAttrs(t *testing.T, cleaned []slog.Attr) {
	t.Helper()
	if len(cleaned) != 2 {
		t.Fatalf("unexpected sanitized attrs: %#v", cleaned)
	}
	if cleaned[0].Key != "" || cleaned[1].Key != "" {
		t.Fatalf("unexpected sanitized keys: %#v", cleaned)
	}
	checkSanitizedGroup(t, cleaned[0].Value.Group())
	nested := cleaned[1].Value.Any().(map[string]any)[nestedKey].([]any)
	if nested[0] != "***" || nested[1] != json.Number("9007199254740993") {
		t.Fatalf("unnamed nested value was not preserved: %#v", nested)
	}
}

func checkSanitizedGroup(t *testing.T, group []slog.Attr) {
	t.Helper()
	if len(group) != 1 {
		t.Fatalf("inline group was not sanitized: %#v", group)
	}
	if group[0].Key != "safe" || group[0].Value.String() != "***" {
		t.Fatalf("inline group was not sanitized: %#v", group)
	}
}

func checkSanitizerInput(t *testing.T, input []slog.Attr) {
	t.Helper()
	original := input[2].Value.Any().(map[string]any)[nestedKey].([]any)
	if input[0].Key != slog.LevelKey || len(input[1].Value.Group()) != 2 {
		t.Fatalf("input attrs were mutated: %#v", input)
	}
	if original[0] != testSecret || original[1] != int64(9007199254740993) {
		t.Fatalf("input value was mutated: %#v", input)
	}
}

func TestSanitizeAttrsNilRedactor(t *testing.T) {
	cleaned := SanitizeAttrs(
		nil,
		[]slog.Attr{slog.String("url", "https://user:password@example.com/path")},
	)
	if len(cleaned) != 1 || strings.Contains(cleaned[0].Value.String(), "password") {
		t.Fatalf("credential was not masked: %#v", cleaned)
	}
}

func TestReservedValuesAreResolvedOnlyWhenRetained(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string
		nested bool
		policy ReservedKeys
		want   int
	}{
		{name: "drop", key: slog.LevelKey, want: 0},
		{name: "accepted", key: "ordinary", want: 1},
		{name: nestedKey, key: slog.LevelKey, nested: true, want: 1},
		{name: "escape", key: slog.LevelKey, policy: EscapeReserved, want: 1},
	} {
		for _, bound := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bound=%t", tc.name, bound), func(t *testing.T) {
				var output bytes.Buffer
				calls := 0
				value := countingValue{calls: &calls, value: map[string]any{"value": "safe"}}
				attr := slog.Any(tc.key, value)
				if tc.nested {
					attr = slog.Group("outer", attr)
				}
				handler := NewHandler(&output, HandlerOptions{
					Format: FormatJSON, ReservedKeys: tc.policy,
				})
				record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
				if bound {
					handler = handler.WithAttrs([]slog.Attr{attr})
				} else {
					record.AddAttrs(attr)
				}
				if err := handler.Handle(context.Background(), record); err != nil {
					t.Fatal(err)
				}
				if calls != tc.want {
					t.Fatalf("LogValue calls = %d, want %d", calls, tc.want)
				}
			})
		}
	}
}

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
	testUserPrefix = "user_"
	testMask       = "***"
	testSecret     = "secret-value"
	nestedKey      = "nested"
	testDebug      = "DEBUG"
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
	if nested[0] != testMask || nested[1] != json.Number("9007199254740993") {
		t.Fatalf("unnamed nested value was not preserved: %#v", nested)
	}
}

func checkSanitizedGroup(t *testing.T, group []slog.Attr) {
	t.Helper()
	if len(group) != 1 {
		t.Fatalf("inline group was not sanitized: %#v", group)
	}
	if group[0].Key != "safe" || group[0].Value.String() != testMask {
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

func TestColoredTextWireOutput(t *testing.T) {
	for _, tc := range []struct {
		level slog.Level
		want  string
	}{
		{LevelTrace, "level=\x1b[36mTRACE\x1b[0m"},
		{slog.LevelDebug, "level=\x1b[36mDEBUG\x1b[0m"},
		{slog.LevelInfo, "level=\x1b[36mINFO\x1b[0m"},
		{slog.LevelWarn, "level=\x1b[33mWARN\x1b[0m"},
		{slog.LevelError, "level=\x1b[31mERROR\x1b[0m"},
		{LevelFatal, "level=\x1b[31mFATAL\x1b[0m"},
	} {
		for _, timestamp := range []time.Time{
			{},
			time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		} {
			checkColoredText(t, tc.level, tc.want, timestamp)
		}
	}
}

func checkColoredText(t *testing.T, level slog.Level, want string, timestamp time.Time) {
	t.Helper()
	var output bytes.Buffer
	handler := NewHandler(&output, HandlerOptions{
		Level: LevelTrace, Format: FormatText, TextStyle: TextColored,
	})
	record := slog.NewRecord(timestamp, level, "user \x1b[31m", 0)
	record.AddAttrs(slog.String("value", "\x1b[32m"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, want) || strings.Count(got, "\x1b") != 2 {
		t.Fatalf("unexpected terminal output %q", got)
	}
	if !strings.Contains(got, `\x1b[31m`) || !strings.Contains(got, `\x1b[32m`) {
		t.Fatalf("user escapes were not quoted: %q", got)
	}
}

type limitedColorWriter struct {
	limit int
	err   error
}

func (w limitedColorWriter) Write(p []byte) (int, error) {
	return min(w.limit, len(p)), w.err
}

func TestColorWriterCountsAndErrors(t *testing.T) {
	input := []byte("level=INFO msg=hello\n")
	failure := fmt.Errorf("sink failure")
	for _, tc := range []struct{ limit, want int }{
		{2, 2}, {8, 6}, {13, 8}, {17, 10}, {100, len(input)},
	} {
		writer := colorWriter{writer: limitedColorWriter{limit: tc.limit, err: failure}}
		n, err := writer.Write(input)
		if n != tc.want || err != failure {
			t.Fatalf("limit %d: count %d, error %v", tc.limit, n, err)
		}
	}
}

type trustedGroupValue struct {
	calls *int
}

func (v trustedGroupValue) LogValue() slog.Value {
	*v.calls++
	return slog.GroupValue(slog.String("provider", "trusted"))
}

func TestInlineTrustedGroupsReserveRootKeys(t *testing.T) {
	for _, policy := range []ReservedKeys{DropReserved, EscapeReserved} {
		var output bytes.Buffer
		calls := 0
		handler := NewHandler(&output, HandlerOptions{
			Format: FormatJSON, ReservedKeys: policy,
			TrustedAttrs: []slog.Attr{
				slog.Group("", slog.Any("", trustedGroupValue{calls: &calls})),
				slog.Group("scope", slog.String("scoped", "trusted")),
			},
		})
		handler = handler.WithAttrs([]slog.Attr{slog.String("provider", "bound-forged")})
		record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
		record.AddAttrs(slog.String("provider", "record-forged"), slog.String("scoped", "root"))
		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
		got := output.String()
		checkTrustedIdentity(t, got, calls)
		checkTrustedPolicy(t, got, policy)
	}
}

func checkTrustedIdentity(t *testing.T, got string, calls int) {
	t.Helper()
	if calls != 1 || strings.Count(got, `"provider":`) != 1 {
		t.Fatalf("trusted key or resolution failed: calls=%d output=%s", calls, got)
	}
	if !strings.Contains(got, `"provider":"trusted"`) || !strings.Contains(got, `"scoped":"root"`) {
		t.Fatalf("trusted or scoped attribute lost: %s", got)
	}
}

func checkTrustedPolicy(t *testing.T, got string, policy ReservedKeys) {
	t.Helper()
	if policy == DropReserved && strings.Contains(got, "forged") {
		t.Fatalf("reserved attribute leaked: %s", got)
	}
	if policy == EscapeReserved && !strings.Contains(got, `"user_provider":"record-forged"`) {
		t.Fatalf("reserved attribute was not escaped: %s", got)
	}
}

func TestStructuredAuthorizationRedaction(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(&output, HandlerOptions{Format: FormatJSON})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	record.AddAttrs(
		slog.String("Authorization", "  bAsIc\tfixture, safe"),
		slog.Any("nested", map[string]any{"authorization": []any{"Bearer opaque", "Basic token"}}),
		slog.Group("headers", slog.String("Authorization", "Bearer group-secret")),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, secret := range []string{"fixture", "opaque", "token", "group-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("structured credential leaked: %s", got)
		}
	}
	if !json.Valid([]byte(got)) || strings.Count(got, `"***"`) != 3 {
		t.Fatalf("credential values must be whole masks: %s", got)
	}
}

func TestDurationAttributesMaskRegisteredRepresentations(t *testing.T) {
	var out bytes.Buffer
	redactor := redact.New(redact.Options{Values: []string{"731942"}})
	h := NewHandler(&out, HandlerOptions{Redactor: redactor, Format: FormatJSON})
	slog.New(h).Info("message",
		slog.Duration("masked", 731942*time.Nanosecond),
		slog.Duration("clear", 42*time.Nanosecond),
	)
	var attrs map[string]any
	if err := json.Unmarshal(out.Bytes(), &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["masked"] != "***" || attrs["clear"] != float64(42) {
		t.Fatalf("unexpected serialized duration attributes: %s", out.String())
	}
}

func TestStructuredCredentialContainers(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(&output, HandlerOptions{Format: FormatJSON}))
	logger.Info(
		"message",
		slog.Any("Cookie", map[string]string{"key": "fixture-map"}),
		slog.Any(
			"parent",
			map[string]any{"Proxy-Authorization": []any{map[string]string{"key": "fixture-array"}}},
		),
		slog.Group("Authorization", slog.String("key", "fixture-group")),
	)
	logger.WithGroup("Set-Cookie").
		With("secret", "fixture-bound").
		Info("second", "key", "fixture-record")
	got := output.String()
	if strings.Contains(got, "fixture-") {
		t.Fatalf("structured credential leaked: %s", got)
	}
	for _, want := range []string{
		`"Cookie":"***"`, `"Proxy-Authorization":"***"`, `"Authorization":"***"`, `"Set-Cookie":"***"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestStructuredAuthorizationKnownOverlap(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(&output, HandlerOptions{
		Format: FormatJSON, Redactor: redact.New(redact.Options{Values: []string{"Bearer"}}),
	})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("Authorization", "Bearer opaque"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"Authorization":"***"`) {
		t.Fatalf("overlapping redaction failed: %s", output.String())
	}
}

type structuredDiagnosticError struct{}

func (structuredDiagnosticError) Error() string {
	return "fallback secret-value"
}

func (structuredDiagnosticError) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"code":          "backend_failure",
		"message":       "failed secret-value",
		"Authorization": "Bearer fresh-token",
	})
}

func TestStructuredErrorJSONAndPlainErrorRedaction(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(&output, HandlerOptions{
		Format: FormatJSON, Redactor: redact.New(redact.Options{Values: []string{testSecret}}),
	})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	record.AddAttrs(
		slog.Any("structured", structuredDiagnosticError{}),
		slog.Any("plain", fmt.Errorf("plain %s", testSecret)),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	structured, ok := got["structured"].(map[string]any)
	if !ok {
		t.Fatalf("structured error was not an object: %s", output.String())
	}
	if structured["code"] != "backend_failure" ||
		structured["message"] != testMask ||
		structured["Authorization"] != testMask {
		t.Fatalf("unexpected structured error: %s", output.String())
	}
	if got["plain"] != testMask {
		t.Fatalf("plain error was not redacted: %s", output.String())
	}
}

func TestTrustedKeyUsesItsSanitizedName(t *testing.T) {
	var output bytes.Buffer
	const sanitized = testMask
	logger := slog.New(NewHandler(&output, HandlerOptions{
		Format: FormatJSON, Redactor: redact.New(redact.Options{Values: []string{testSecret}}),
		TrustedAttrs: []slog.Attr{slog.String("root-"+testSecret, "trusted")},
	}))
	logger.Info("record", slog.String(sanitized, "forged"))
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got[sanitized] != "trusted" || strings.Contains(output.String(), testSecret) {
		t.Fatalf("trusted key was exposed or overwritten: %s", output.String())
	}
}

func TestTextGroupsProtectFlattenedTrustedKeys(t *testing.T) {
	for _, format := range []Format{FormatText, FormatLogfmt, FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			const trusted = "meta.provider"
			logger := slog.New(NewHandler(&output, HandlerOptions{
				Format: format, TrustedAttrs: []slog.Attr{slog.String(trusted, "trusted")},
			}))
			logger.Info("record", slog.Group("meta", slog.String("provider", "forged")))
			logger.WithGroup("meta").With("provider", "forged").Info("bound")
			got := output.String()
			if format == FormatJSON {
				if strings.Count(got, `"meta":{"provider":"forged"}`) != 2 {
					t.Fatalf("distinct JSON groups were discarded: %s", got)
				}
			} else if strings.Contains(got, "forged") || strings.Count(got, trusted+"=trusted") != 2 {
				t.Fatalf("flattened trusted key was overwritten: %s", got)
			}
		})
	}
}

func TestEscapedReservedAliasDoesNotOverwriteAnotherAlias(t *testing.T) {
	var output bytes.Buffer
	reserved := testUserPrefix + slog.LevelKey
	logger := slog.New(NewHandler(&output, HandlerOptions{
		Format:       FormatJSON,
		ReservedKeys: EscapeReserved,
		TrustedAttrs: []slog.Attr{
			slog.String(reserved, "trusted one"),
			slog.String(testUserPrefix+reserved, "trusted two"),
		},
	}))
	logger.Info(
		"record",
		slog.String(slog.LevelKey, "emitted level"),
		slog.String(reserved, "emitted alias"),
	)
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got[strings.Repeat(testUserPrefix, 3)+slog.LevelKey] != "emitted level" ||
		got[strings.Repeat(testUserPrefix, 4)+slog.LevelKey] != "emitted alias" {
		t.Fatalf("escaped aliases collided: %s", output.String())
	}
}

func TestKnownNumbersAreRedactedWithoutChangingOtherTypes(t *testing.T) {
	var output bytes.Buffer
	const code = 731942
	const large = uint64(9007199254740993)
	logger := slog.New(NewHandler(&output, HandlerOptions{
		Format:   FormatJSON,
		Redactor: redact.New(redact.Options{Values: []string{"731942", "1000000"}}),
	}))
	logger.Info(
		"record",
		slog.Int("number", code),
		slog.Float64("float", 1e6),
		slog.Uint64("unchanged", large),
		slog.Any(nestedKey, []any{code, large}),
	)
	decoder := json.NewDecoder(&output)
	decoder.UseNumber()
	var got map[string]any
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	nested := got[nestedKey].([]any)
	if got["number"] != testMask || got["float"] != testMask || nested[0] != testMask ||
		got["unchanged"] != json.Number("9007199254740993") || nested[1] != got["unchanged"] {
		t.Fatalf("numeric redaction or precision was lost: %#v", got)
	}
}

func TestNestedValuesAndCollidingKeysAreRedacted(t *testing.T) {
	var output bytes.Buffer
	const otherSecret = "another-secret"
	logger := slog.New(NewHandler(&output, HandlerOptions{
		Format:   FormatJSON,
		Redactor: redact.New(redact.Options{Values: []string{testSecret, otherSecret}}),
	}))
	logger.Info("record", slog.Any(nestedKey, map[string]any{
		testSecret:      "before-" + testSecret + "-after",
		otherSecret:     "collision",
		testMask + "_1": "existing",
	}))
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	nested := got[nestedKey].(map[string]any)
	value, ok := nested[testMask+"_2"].(string)
	if nested[testMask] != "collision" || nested[testMask+"_1"] != "existing" || !ok {
		t.Fatalf("colliding object keys were lost: %#v", nested)
	}
	if value != testMask {
		t.Fatalf("nested string was not replaced by a whole mask: %q", value)
	}
}

const testSecretDate = "1984-05-17"

func TestTimeAndBooleanAttributesMaskRegisteredValues(t *testing.T) {
	var output bytes.Buffer
	r := redact.New(redact.Options{Values: []string{testSecretDate, "true"}})
	logger := slog.New(NewHandler(&output, HandlerOptions{Format: FormatJSON, Redactor: r}))
	logger.Info("record",
		slog.Time("date", time.Date(1984, 5, 17, 0, 0, 0, 0, time.UTC)),
		slog.Time("ordinary_date", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)),
		slog.Bool("known", true),
		slog.Bool("ordinary", false),
	)
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["date"] != testMask || got["ordinary_date"] != "2025-02-01T00:00:00Z" ||
		got["known"] != testMask || got["ordinary"] != false {
		t.Fatalf("scalar masking lost the contract: %#v", got)
	}
}

func TestRegisteredWireFormsInHandler(t *testing.T) {
	const fragment = `{ "credential" : "private-json-string" }`
	var output bytes.Buffer
	r := redact.New(redact.Options{Values: []string{fragment, `a\u0000b`}})
	logger := slog.New(NewHandler(&output, HandlerOptions{Format: FormatJSON, Redactor: r}))
	logger.Info("a\x00b", "document", fragment, "wire", "a\x00b")
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["msg"] != testMask || got["document"] != testMask || got["wire"] != testMask {
		t.Fatalf("registered wire value was not masked: %#v", got)
	}
}

func TestStructuredAttributesMaskRegisteredDocuments(t *testing.T) {
	const (
		large    = "9007199254740993"
		ordinary = "ordinary_payload"
	)
	cases := map[string]string{
		"standard JSON":  `{"credential":"synthetic-private-value"}`,
		"unescaped HTML": `{"credential":"synthetic-private-<value>"}`,
		"escaped HTML":   `{"credential":"synthetic-private-\u003cvalue\u003e"}`,
		"separate match": `{"credential":"synthetic-private-<value>","other":"second-registration"}`,
	}
	for name, secret := range cases {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(secret), &payload); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			logger := slog.New(NewHandler(&output, HandlerOptions{
				Format: FormatJSON,
				Redactor: redact.New(
					redact.Options{Values: []string{secret, "second-registration"}},
				),
			}))
			matching := slog.Any(nestedKey, payload)
			preserved := slog.Any(ordinary, map[string]any{"number": json.Number(large)})
			logger.Info("record", matching, preserved)
			logger.With(matching, preserved).Info("bound")

			decoder := json.NewDecoder(&output)
			decoder.UseNumber()
			for range 2 {
				var got map[string]any
				if err := decoder.Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got[nestedKey] != testMask {
					t.Errorf("registered document was emitted: %#v", got[nestedKey])
				}
				regular := got[ordinary].(map[string]any)
				if regular["number"] != json.Number(large) {
					t.Fatalf("ordinary object lost integer precision: %#v", regular)
				}
			}
		})
	}
}

func TestNestedCollisionNeverCreatesARegisteredValue(t *testing.T) {
	const alias = "synthetic-mask_1"
	const mask = "synthetic-mask"
	const sibling = "public_sibling"
	const field = "aliased_object"
	var output bytes.Buffer
	logger := slog.New(NewHandler(&output, HandlerOptions{
		Format:   FormatJSON,
		Redactor: redact.New(redact.Options{Values: []string{alias}, Mask: mask}),
	}))
	logger.Info("record", slog.Any(nestedKey, map[string]any{
		field:   map[string]any{"a-" + alias: true, "b-" + alias: false},
		sibling: "visible",
	}))
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	nested, ok := got[nestedKey].(map[string]any)
	if !ok || nested[field] != mask || nested[sibling] != "visible" ||
		strings.Contains(output.String(), alias) {
		t.Fatalf("unsafe alias affected the surrounding record: %s", output.String())
	}
}

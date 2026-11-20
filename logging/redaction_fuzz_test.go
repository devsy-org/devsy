package logging

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
)

const (
	fuzzURL     = "https://user:"
	fuzzURLHost = "@example.test/path"
)

func FuzzCredentialRedaction(f *testing.F) {
	shapes := []string{
		"json object", "json arrays", "escaped name", "literal backslash key",
		"deep container", "embedded header", "blank HTTP line", "escaped URL slashes",
		"escaped URL authority", "long scheme and first escape", "overlong record",
		"opaque backslash", "ordinary prefix key", "numeric scalar", "time attribute",
		"duration attribute", "trusted key", "nested key collision", "encoded bytes",
		"opaque header separator", "json string escapes", "non-http scheme",
	}
	for shape, name := range shapes {
		f.Add(uint8(shape), uint16(shape+1), []byte(name))
	}
	f.Fuzz(func(t *testing.T, shape uint8, split uint16, seed []byte) {
		digest := sha256.Sum256(seed)
		secret := "fixture-" + hex.EncodeToString(digest[:12])
		input := fuzzInput(int(shape)%len(shapes), secret)
		assertFuzzText(t, input, secret, int(split))
		assertFuzzAttrs(t, secret)
	})
}

func fuzzInput(shape int, secret string) string {
	forms := []func(string) string{
		func(value string) string { return `{"Cookie":"` + value + `","note":"public"}` },
		func(value string) string { return `{"Authorization":[true,42,["` + value + `"]]}` },
		func(value string) string { return `{"\u0041uthorization":"` + value + `"}` },
		func(value string) string { return `{"\\u0041uthorization":"public ` + value + `"}` },
		func(value string) string {
			return `{"Cookie":` + strings.Repeat(
				"[",
				65,
			) + `"` + value + `"` + strings.Repeat(
				"]",
				65,
			) + `}`
		},
		func(value string) string { return `{"msg":"sent Authorization: ` + value + `","note":"public"}` },
		func(value string) string { return "Authorization: " + value + "\r\n\r\n public body" },
		func(value string) string { return `{"url":"https:\/\/user:` + value + fuzzURLHost + `"}` },
		func(value string) string { return `{"url":"https://u\u0026ser:` + value + fuzzURLHost + `"}` },
		func(value string) string {
			return `{"url":"` + strings.Repeat(
				"x",
				600,
			) + `://\u0026user:` + value + fuzzURLHost + `"}`
		},
		func(value string) string { return fuzzURL + strings.Repeat("w", 17*1024) + value + fuzzURLHost },
		func(value string) string { return fuzzURL + value + `@host\contact@other.test` },
		func(value string) string { return `{"X-Authorization":"public ` + value + `"}` },
		func(value string) string { return `{"number":731942,"value":"` + value + `"}` },
		func(value string) string { return `{"date":"1984-05-17T00:00:00Z","value":"` + value + `"}` },
		func(value string) string { return `{"duration":731942,"value":"` + value + `"}` },
		func(value string) string { return `{"` + value + `":"trusted key"}` },
		func(value string) string { return `{"nested":{"` + value + `":"a","***":"b"}}` },
		func(value string) string { return `{"bytes":"cHVibGlj","value":"` + value + `"}` },
		func(value string) string { return "AUTHORIZATION = Bearer\t" + value },
		func(value string) string { return `{"Authorization":"Bearer\t` + value + `\u0020\""}` },
		func(value string) string { return "git+ssh://user:" + value + fuzzURLHost },
	}
	return forms[shape](secret)
}

func assertFuzzText(t *testing.T, input, secret string, split int) {
	t.Helper()
	r := redact.New(redact.Options{Values: []string{secret}})
	whole := r.Redact(input)
	stream := redact.NewStream(r)
	cut := split % (len(input) + 1)
	streamed := stream.RedactChunk(input[:cut]) + stream.RedactChunk(input[cut:]) + stream.Flush()
	for _, output := range []string{whole, streamed} {
		if strings.Contains(output, secret) ||
			json.Valid([]byte(input)) && !json.Valid([]byte(output)) {
			t.Fatal("a registered secret survived or valid JSON became invalid")
		}
	}
	if len(input) <= 16*1024 && !strings.ContainsAny(input, "\r\n") && whole != streamed {
		t.Fatal("chunking changed a complete diagnostic record")
	}
}

func assertFuzzAttrs(t *testing.T, secret string) {
	t.Helper()
	var out bytes.Buffer
	when := time.Date(1984, 5, 17, 0, 0, 0, 0, time.UTC)
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	const field = "private_document"
	const second = "secondary"
	const alias = testMask + "_1"
	private := "<composite-" + strings.TrimPrefix(secret, "fixture-") + ">"
	document := `{"` + field + `":"` + private + `","` + second + `":"` + secret + `"}`
	registered := []string{secret, encoded, testSecretDate, "731942", document, alias}
	r := redact.New(redact.Options{Values: registered})
	logger := slog.New(NewHandler(&out, HandlerOptions{Format: FormatJSON, Redactor: r}))
	logger.Info("record",
		slog.Group("nested", slog.String("Authorization", secret), slog.String("visible", secret)),
		slog.String(secret, "key"),
		slog.Any("encoded", []byte(secret)),
		slog.Time("date", when),
		slog.Duration("elapsed", 731942*time.Nanosecond),
		slog.Any("document", map[string]string{field: private, second: secret}),
		slog.Any("collision", map[string]bool{"a-" + alias: true, "b-" + alias: false}),
	)
	if !json.Valid(out.Bytes()) {
		t.Fatal("attribute redaction damaged JSON")
	}
	for _, value := range append(registered, private) {
		if strings.Contains(out.String(), value) {
			t.Fatal("attribute redaction leaked a registered value")
		}
	}
}

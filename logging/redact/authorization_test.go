package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	authFixtureOne    = "redaction-fixture-one-2471"
	authFixtureTwo    = "redaction-fixture-two-9685"
	authFixturePublic = "unrelated-visible-field"
)

type credentialCase struct {
	name  string
	input string
	json  bool
}

func structuredCredentialCases() []credentialCase {
	return []credentialCase{
		{
			"json scalar",
			`{"Authorization":"Bearer\t` + authFixtureOne + `","note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"json array",
			`{"Authorization":["Bearer ` + authFixtureOne + `","Basic ` + authFixtureTwo +
				`"],"note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"key escapes",
			`{"\u0041uTh\u006frization":"Basic\u0020` + authFixtureOne + `","note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"escaped quote",
			`{"Authorization":"escaped \"` + authFixtureOne + `\\\" ` + authFixtureTwo + `","note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"nested fields",
			`{"headers":{"Proxy-Authorization":{"value":["` + authFixtureOne + `",{"nested":"` + authFixtureTwo +
				`"}]}},"note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"multiline json field",
			"{\"Cookie\"\r\n :\n [\"" + authFixtureOne + `"],"note":"` + authFixturePublic + `"}`,
			true,
		},
		{
			"cookie array",
			`{"Set-Cookie":["x=` + authFixtureOne + `; Secure","y=` + authFixtureTwo + `"],"note":"` + authFixturePublic + `"}`,
			true,
		},
	}
}

func textCredentialCases() []credentialCase {
	return []credentialCase{
		{"logfmt token", "Cookie=x=" + authFixtureOne + " note=" + authFixturePublic, false},
		{
			"logfmt quoted",
			`Proxy-Authorization="Custom ` + authFixtureOne + `\" ` + authFixtureTwo + `" note=` + authFixturePublic,
			false,
		},
		{
			"escaped diagnostic field",
			`\"Authorization\":\"Bearer\t` + authFixtureOne + `,]} ` + authFixtureTwo + `\"` + "\n" + authFixturePublic,
			false,
		},
		{
			"folded header",
			"Cookie: x=" + authFixtureOne + ";\r\n\ty=" + authFixtureTwo + "\r\nX-Trace: " + authFixturePublic,
			false,
		},
		{
			"quoted header line",
			"Set-Cookie: x=\"" + authFixtureOne + "\"; y=" + authFixtureTwo + "\n" + authFixturePublic,
			false,
		},
		{
			"unquoted scheme",
			"Authorization=Basic " + authFixtureOne + "\n" + authFixturePublic,
			false,
		},
		{
			"escaped compound field",
			`\"Proxy-Authorization\":[\"` + authFixtureOne + `\",{\"label\":\"` + authFixtureTwo +
				`\"}]` + "\n" + authFixturePublic,
			false,
		},
		{
			"escaped unicode key",
			`\"\\u0043ookie\":\"` + authFixtureOne + `,` + authFixtureTwo + `\"` + "\n" + authFixturePublic,
			false,
		},
		{
			"escaped multiline field",
			`\"Cookie\"\r\n:` + "\n" + `\"` + authFixtureOne + `\"` + "\n" + authFixturePublic,
			false,
		},
		{"malformed container", `{"Cookie":[}` + authFixtureOne + "\n" + authFixtureTwo, false},
	}
}

func TestSerializedCredentialFieldBoundaries(t *testing.T) {
	redactor := New(Options{Values: []string{"unrelated-known-value"}})
	for _, tt := range append(structuredCredentialCases(), textCredentialCases()...) {
		t.Run(tt.name, func(t *testing.T) {
			assertCredentialField(t, redactor, tt.input, tt.json)
		})
	}
}

func assertCredentialField(t *testing.T, redactor *Redactor, input string, isJSON bool) {
	t.Helper()
	want := redactor.Redact(input)
	if strings.Contains(want, authFixtureOne) || strings.Contains(want, authFixtureTwo) {
		t.Fatal("credential survived whole-message masking")
	}
	if strings.Contains(input, authFixturePublic) && !strings.Contains(want, authFixturePublic) {
		t.Fatal("unrelated field was lost")
	}
	if isJSON && !json.Valid([]byte(want)) {
		t.Fatal("valid string-based JSON lost its structure")
	}
	assertAllStreamCuts(t, redactor, input, want)
	got, _ := streamChunks(NewStream(redactor), input, []int{1})
	if got != want {
		t.Fatal("bytewise stream and whole-message masking differ")
	}
}

func TestCredentialJSONStructureAcrossSplits(t *testing.T) {
	redactor := New(Options{Values: []string{"unrelated-known-value"}})
	for input, want := range map[string]string{
		`{"X-Authorization":"value","note":"ok"}`:                `{"X-Authorization":"value","note":"ok"}`,
		`{"msg":"sent Authorization: private","note":"ok"}`:      `{"msg":"sent Authorization: ***","note":"ok"}`,
		`{"msg":"sent Cookie: private\"tail","note":"ok"}`:       `{"msg":"sent Cookie: ***","note":"ok"}`,
		`{"Authorization":123,"note":"ok"}`:                      `{"Authorization":null,"note":"ok"}`,
		`{"Authorization":[123,true,null,"secret"],"note":"ok"}`: `{"Authorization":[null,null,null,"***"],"note":"ok"}`,
		`{"Cookie":{"number":99,"value":[false]},"note":"ok"}`:   `{"Cookie":{"***":null,"***":[null]},"note":"ok"}`,
	} {
		got := redactor.Redact(input)
		if got != want || !json.Valid([]byte(got)) {
			t.Fatalf("redacted %q, want valid JSON %q", got, want)
		}
		assertAllStreamCuts(t, redactor, input, want)
		got, _ = streamChunks(NewStream(redactor), input, []int{1})
		if got != want {
			t.Fatalf("bytewise stream redacted %q, want %q", got, want)
		}
	}
}

func TestJSONCredentialMaskEscapesCustomMarker(t *testing.T) {
	mask := "hidden\"\n"
	redactor := New(Options{Mask: mask})
	input := `{"Authorization":"credentials"}`
	encoded := redactor.Redact(input)
	var record map[string]string
	if err := json.Unmarshal(
		[]byte(encoded),
		&record,
	); err != nil ||
		record["Authorization"] != mask {
		t.Fatalf("serialized custom marker %q: %v", encoded, err)
	}
	assertAllStreamCuts(t, redactor, input, encoded)
}

func TestCredentialTokenizerBoundsAndResets(t *testing.T) {
	redactor := New(Options{Values: []string{strings.Repeat("z", 2048) + "x"}})
	for _, input := range []string{
		`{"Authorization":"` + strings.Repeat("z", 10000) + `"}`,
		`{"Cookie":` + strings.Repeat("[", maxHeaderDepth+1) + strings.Repeat("z", 10000),
	} {
		stream := NewStream(redactor)
		for offset := 0; offset < len(input); offset += 7 {
			got := stream.RedactChunk(input[offset:min(offset+7, len(input))])
			if strings.Contains(got, "zzz") || len(stream.pending) > 2*redactor.maxLength {
				t.Fatal("stream retained or emitted credential data")
			}
		}
		if got := stream.Flush(); strings.Contains(got, "zzz") {
			t.Fatal("flush emitted credential data")
		}
		if got := stream.RedactChunk("normal output"); got != "normal output" {
			t.Fatal("flush did not reset credential context")
		}
	}
}

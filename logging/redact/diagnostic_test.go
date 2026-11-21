package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticURLAuthority(t *testing.T) {
	tests := []struct{ input, want string }{
		{
			"ssh://user:fixture%40secret@node.test/run/podman.sock; password=fixture@secret",
			"ssh://***@node.test/run/podman.sock; password=fixture@secret",
		},
		{
			"ssh://node.test/path/@name?email=repo@example.test",
			"ssh://node.test/path/@name?email=repo@example.test",
		},
		{"ssh://node.test?email=repo@example.test", "ssh://node.test?email=repo@example.test"},
		{"ssh://node.test#name@example.test", "ssh://node.test#name@example.test"},
		{"ssh://user:fixture@secret@node.test/path", "ssh://***@node.test/path"},
		{"ssh://node.test; email repo@example.test", "ssh://node.test; email repo@example.test"},
		{
			`endpoint="ssh://u:fixture@host.test",notify="repo@example.test"`,
			`endpoint="ssh://***@host.test",notify="repo@example.test"`,
		},
		{
			"ssh://u:fixture@host.test\u00a0repo@example.test",
			"ssh://***@host.test\u00a0repo@example.test",
		},
	}
	for _, test := range tests {
		if got := (*Redactor)(nil).RedactDiagnostic(test.input); got != test.want {
			t.Fatalf("redacted URL = %q, want %q", got, test.want)
		}
	}
}

func TestDiagnosticFoldedAuthorization(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"request Authorization:\n Bearer fixture\npublic", "request Authorization:***\npublic"},
		{"Authorization: \r\nBasic fixture\r\nserver: public", "Authorization: ***\r\nserver: public"},
		{"Proxy-Authorization: Digest fixture\r\n\tcontinued fixture\npublic", "Proxy-Authorization: ***\npublic"},
		{"Authorization:\n\n  public body", "Authorization:\n\n  public body"},
		{"Authorization: Bearer fixture\r\n \t\r\n  public body", "Authorization: ***\r\n \t\r\n  public body"},
		{`{"msg":"Authorization:\n Bearer fixture","safe":true}`, `{"msg":"Authorization:***","safe":true}`},
	} {
		if got := (*Redactor)(nil).RedactDiagnostic(test.input); got != test.want {
			t.Fatalf("redacted diagnostic = %q, want %q", got, test.want)
		}
	}
	redactor := New(Options{})
	const input = "Authorization:\n Bearer fixture"
	if got := redactor.Redact(input); got != input {
		t.Fatalf("non-opt-in text changed: %q", got)
	}
}

func TestDiagnosticStreamPreservesBoundedTail(t *testing.T) {
	redactor := New(Options{Values: []string{testLiteralSecret, testRepeatedSecret}})
	text := strings.Repeat("x", 2<<20)
	stream := NewDiagnosticStream(redactor)
	before := stream.RedactChunk(text)
	if before != text {
		t.Fatal("oversized plain diagnostic did not emit its safe bytes promptly")
	}
	parts := []string{testLiteralSecret[:3], testLiteralSecret[3:], "\r\n"}
	var after strings.Builder
	for _, part := range parts {
		after.WriteString(stream.RedactChunk(part))
	}
	after.WriteString(stream.Flush())
	if got := after.String(); got != "***\r\n" {
		t.Fatalf("tail = %q", got)
	}
	repeated := strings.Repeat(testRepeatedSecret, streamLineLimit)
	prompt := stream.RedactChunk(repeated)
	if len(prompt) < 3*(streamLineLimit-1) ||
		prompt+stream.Flush() != redactor.RedactDiagnostic(repeated) {
		t.Fatal("streaming repeatedly matched diagnostics lost prompt output or changed masking")
	}
}

func TestDiagnosticStreamSuppressesAmbiguousSuffix(t *testing.T) {
	r := New(Options{Values: []string{"part:fixture"}})
	prefix := strings.Repeat("x", streamLineLimit) + "ssh"
	for _, split := range []int{0, len(prefix) - 2, len(prefix), len(prefix) + 1} {
		input := prefix + "://user:fixture@host.test\n"
		stream := NewDiagnosticStream(r)
		got := stream.RedactChunk(
			input[:split],
		) + stream.RedactChunk(
			input[split:],
		) + stream.Flush()
		if got != prefix+"***\n" {
			t.Fatalf("split %d: oversized URL credential or safe prefix was mishandled", split)
		}
	}
	stream := NewDiagnosticStream(r)
	if got := stream.RedactChunk(
		prefix+" part",
	) + stream.RedactChunk(
		":fixture\n",
	); got != prefix+" ***\n" {
		t.Fatalf("registered value crossing the suppression boundary leaked")
	}
}

func TestDiagnosticStreamKeepsConservativeFallbacks(t *testing.T) {
	r := New(Options{})
	for _, input := range []string{
		`{"Authorization":"` + strings.Repeat("fixture", streamLineLimit) + `"}`,
		"Authorization:" + strings.Repeat("fixture", streamLineLimit),
	} {
		stream := NewDiagnosticStream(r)
		if got := stream.RedactChunk(
			input,
		) + stream.Flush(); got != `"***"` ||
			!json.Valid([]byte(got)) {
			t.Fatal("oversized structured diagnostic lost conservative masking")
		}
	}
	r = New(Options{Values: []string{"part\nfixture"}})
	stream := NewDiagnosticStream(r)
	if got := stream.RedactChunk(
		strings.Repeat("x", streamLineLimit)+"part\nfixture\n",
	) + stream.Flush(); got != "\"***\"\n\"***\"\n" {
		t.Fatal("multi-record registered value escaped conservative masking")
	}
}

func TestDiagnosticStreamMaskCannotRecreateRegisteredBytes(t *testing.T) {
	redactor := New(Options{Mask: "é", Values: []string{testLiteralSecret, "\xA9x"}})
	stream := NewDiagnosticStream(redactor)
	value := strings.Repeat("p", streamLineLimit) + testLiteralSecret + "x"
	got := stream.RedactChunk(value) + stream.Flush()
	if got != redactor.jsonMask() || redactor.ContainsValue(got) {
		t.Fatal("mask combined with ordinary bytes to recreate a registered value")
	}
}

func FuzzDiagnosticStreamPlain(f *testing.F) {
	f.Add("abcabcabab ", uint16(1))
	f.Add("abcabcXabcabcabc ", uint16(3))
	f.Add(" bababa ab ", uint16(4095))
	f.Fuzz(func(t *testing.T, raw string, chunk uint16) {
		const alphabet = "abcX "
		var input strings.Builder
		input.WriteString(strings.Repeat("x", streamLineLimit))
		for index := range min(len(raw), 4*streamLineLimit) {
			input.WriteByte(alphabet[int(raw[index])%len(alphabet)])
		}
		input.WriteString(testSelfOverlap)
		value := input.String()
		redactor := New(
			Options{
				Values: []string{
					testSelfOverlap,
					testOverlapReverse,
					testRepeatedSecret,
					"abcabcX",
				},
			},
		)
		stream := NewDiagnosticStream(redactor)
		var output strings.Builder
		size := int(chunk) + 1
		for start := 0; start < len(value); start += size {
			output.WriteString(stream.RedactChunk(value[start:min(start+size, len(value))]))
		}
		output.WriteString(stream.Flush())
		got := output.String()
		if redactor.ContainsValue(got) || got != redactor.RedactDiagnostic(value) {
			t.Fatal("chunking exposed a registered value or changed the complete output")
		}
	})
}

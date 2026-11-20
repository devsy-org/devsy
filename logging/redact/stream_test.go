package redact

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestStreamKnownSecretEverySplit(t *testing.T) {
	tests := []struct {
		values []string
		input  string
	}{
		{[]string{testRepeatedSecret}, "ordinary abcabc abcabc done"},
		{[]string{"aaaaa"}, "ordinary aaaaa done"},
		{[]string{"abababa"}, "ordinary abababa done"},
		{[]string{"abcabcX", testRepeatedSecret}, "ordinary abcabcX abcabc done"},
		{[]string{testSelfOverlap, testOverlapReverse}, "ordinary abab done"},
		{
			[]string{testShortSecret, testCrossingSecret, testCrossingTail},
			"safe abc-secret-part-tail visible",
		},
		{[]string{"abc\x00\xffabc"}, "ordinary abc\x00\xffabc done"},
		{[]string{testLiteralSecret}, "ordinary no credentials here"},
	}
	for _, test := range tests {
		r := New(Options{Values: test.values})
		assertAllStreamCuts(t, r, test.input, r.Redact(test.input))
	}
}

func referenceLongestMatch(input string, secrets []string) int {
	longest := 0
	for _, secret := range secrets {
		if strings.HasPrefix(input, secret) && len(secret) > longest {
			longest = len(secret)
		}
	}
	return longest
}

func TestNilStreamAndBase(t *testing.T) {
	var stream *Stream
	if got := stream.RedactChunk("unchanged"); got != "unchanged" || stream.Flush() != "" {
		t.Fatal("nil stream changed data")
	}
	stream = NewStream(nil)
	if got := stream.RedactChunk(testCredentialURL()); got != testCredentialURL() ||
		stream.Flush() != "" {
		t.Fatal("nil base changed data")
	}
}

func TestStreamCompletedRecordsAndPartialTail(t *testing.T) {
	stream := NewStream(New(Options{Values: []string{testLiteralSecret}}))
	for _, check := range []struct{ input, output string }{
		{`{"msg":"safe"}` + "\n", `{"msg":"safe"}` + "\n"},
		{"progress 31%", ""},
		{"\rpartial sec", "progress 31%\r"},
		{"ret", ""},
	} {
		if got := stream.RedactChunk(check.input); got != check.output {
			t.Fatalf("got %q, want %q", got, check.output)
		}
	}
	if got := stream.Flush(); got != "partial ***" {
		t.Fatalf("flush = %q", got)
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("repeated flush = %q", got)
	}
}

func TestStreamKnownValueAcrossRecords(t *testing.T) {
	redactor := New(Options{Values: []string{"top\nsecret", "trailing\r"}})
	input := "first\ntop\nsecret tail\ntrailing\rlast\n"
	want := "first\n\"***\"\n\"***\"\n\"***\"\rlast\n"
	assertAllStreamCuts(t, redactor, input, want)
	stream := NewStream(redactor)
	if got := stream.RedactChunk("top\n"); got != "" {
		t.Fatalf("unresolved multiline prefix was emitted: %q", got)
	}
	if got := stream.RedactChunk("visible\n"); got != "top\nvisible\n" {
		t.Fatalf("safe mismatch was not released: %q", got)
	}
}

func TestStreamPatternsAndJSONAcrossSplits(t *testing.T) {
	r := New(Options{})
	jsonLine := `{"msg":"Authorization: fixture","note":"safe","url":"https:\/\/u:fixture@example.test"}`
	for _, input := range []string{
		"before https://user:fixture@example.test/path\n",
		"Authorization: fixture\r\n\r\n  public body\n",
		jsonLine + "\r\n",
	} {
		want := r.Redact(
			strings.TrimRight(input, "\r\n"),
		) + input[len(strings.TrimRight(input, "\r\n")):]
		if strings.HasPrefix(input, "Authorization:") {
			want = "Authorization: ***\r\n\r\n  public body\n"
		}
		if strings.Contains(want, "fixture") {
			t.Fatal("an unregistered fixture was emitted")
		}
		assertAllStreamCuts(t, r, input, want)
	}
}

func TestStreamOversizedRecordsRecoverAtDelimiter(t *testing.T) {
	r := New(Options{})
	for _, input := range []string{
		`{"msg":"` + strings.Repeat("p", streamLineLimit) + ` https://user:fixture@example.test"}`,
		strings.Repeat("s", streamLineLimit) + `://\u0026user:fixture@example.test`,
		"Authorization:" + strings.Repeat(" ", streamLineLimit) + "fixture",
	} {
		stream := NewStream(r)
		var output strings.Builder
		for start := 0; start < len(input); start += 73 {
			output.WriteString(stream.RedactChunk(input[start:min(start+73, len(input))]))
			if len(stream.line) > streamLineLimit {
				t.Fatal("unfinished line exceeded its memory bound")
			}
		}
		output.WriteString(stream.RedactChunk("\nsafe after overflow\n"))
		output.WriteString(stream.Flush())
		masked, after, _ := strings.Cut(output.String(), "\n")
		if masked != `"***"` || !json.Valid([]byte(masked)) ||
			after != "safe after overflow\n" {
			t.Fatal("overflow did not emit one valid mask and recover on the next record")
		}
	}
}

func TestStreamCRLFDoesNotCreatePhantomJSONRecord(t *testing.T) {
	r := New(Options{Values: []string{"tail\"}\r\n{\"head"}})
	input := `{"item":"tail"}` + "\r\n" + `{"head":"value"}` + "\r\n"
	want := "\"***\"\r\n\"***\"\r\n"
	assertAllStreamCuts(t, r, input, want)
	for _, line := range strings.Split(strings.TrimSpace(want), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("output line is invalid JSON")
		}
	}
}

func TestStreamLongSecretCarryBound(t *testing.T) {
	secret := strings.Repeat("part\n", 2000) + "final"
	stream := NewStream(New(Options{Values: []string{secret}}))
	for i := range len(secret) {
		if got := stream.RedactChunk(secret[i : i+1]); strings.Contains(got, "part") {
			t.Fatal("cross-record secret leaked")
		}
		if n := len(stream.records); n > len(secret) {
			t.Fatalf("queued %d records for a %d-byte secret", n, len(secret))
		}
	}
	if got := stream.Flush(); strings.Contains(got, "final") {
		t.Fatal("secret suffix leaked at flush")
	}
}

func TestStreamOffsetsPass32BitBoundary(t *testing.T) {
	stream := NewStream(New(Options{Values: []string{"top\nsecret"}}))
	stream.offset = math.MaxInt32 - 3
	stream.lineStart = stream.offset
	if got := stream.RedactChunk("top\n"); got != "" {
		t.Fatalf("unresolved prefix escaped: %q", got)
	}
	if got := stream.RedactChunk("secret\npublic\n"); got != "\"***\"\n\"***\"\npublic\n" {
		t.Fatalf("record processing stalled at the 32-bit boundary: %q", got)
	}
}

func assertAllStreamCuts(t *testing.T, redactor *Redactor, input, want string) {
	t.Helper()
	for cut := 0; cut <= len(input); cut++ {
		stream := NewStream(redactor)
		got := stream.RedactChunk(input[:cut]) + stream.RedactChunk(input[cut:]) + stream.Flush()
		if got != want {
			t.Fatalf("split %d: got %q, want %q", cut, got, want)
		}
	}
}

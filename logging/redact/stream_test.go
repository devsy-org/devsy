package redact

import (
	"strings"
	"testing"
)

const (
	testURLScheme  = "https://"
	testURLUser    = "user:"
	testURLSuffix  = "@example.test/x"
	testFormatPart = "http:"
	testDenseToken = "xx"
)

const (
	benchComplete = "complete"
	benchMismatch = "mismatch"
	benchFlush    = "flush"
	benchSliding  = "sliding"
	benchDense    = "dense"
	benchFormat   = "format"
	benchURL      = "url"
	benchAuthURL  = "auth_url"
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
		{[]string{"secret"}, "ordinary no credentials here"},
	}
	for _, test := range tests {
		r := New(Options{Values: test.values})
		assertAllStreamCuts(t, r, test.input, r.Redact(test.input))
	}
}

func TestStreamEmitsCompleteDiagnosticAndRetainsAmbiguousSuffix(t *testing.T) {
	r := New(Options{Values: []string{"token", "token12345", "top\nsecret"}})
	stream := NewStream(r)
	diagnostic := "{\"level\":\"info\",\"msg\":\"safe\"}\n"
	if got := stream.RedactChunk(diagnostic); got != diagnostic {
		t.Fatalf("complete diagnostic = %q, want %q", got, diagnostic)
	}
	if got := stream.RedactChunk("prefix token"); got != "prefix " {
		t.Fatalf("ambiguous prefix output = %q", got)
	}
	if got := stream.RedactChunk("12345 end\n"); got != "*** end\n" {
		t.Fatalf("resolved credential output = %q", got)
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("unexpected final output = %q", got)
	}
}

func TestStreamPromptDiagnosticWithLongKnownSecret(t *testing.T) {
	secret := strings.Repeat("x", 65536) + "Z"
	stream := NewStream(New(Options{Values: []string{secret}}))
	diagnostic := "{\"level\":\"info\",\"msg\":\"safe\"}\n"
	if got := stream.RedactChunk(diagnostic); got != diagnostic {
		t.Fatalf("complete diagnostic = %q, want %q", got, diagnostic)
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("unexpected final output = %q", got)
	}
}

func TestStreamRetainsMultilineSecretAcrossNewline(t *testing.T) {
	stream := NewStream(New(Options{Values: []string{"top\nsecret"}}))
	if got := stream.RedactChunk("top\n"); got != "" {
		t.Fatalf("multiline prefix leaked = %q", got)
	}
	if got := stream.RedactChunk("secret end\n"); got != "*** end\n" {
		t.Fatalf("multiline output = %q", got)
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("unexpected final output = %q", got)
	}
}

func TestStreamExhaustiveIntersectingMatches(t *testing.T) {
	values := []string{testSelfOverlap, testOverlapReverse, "bb"}
	r := New(Options{Values: values})
	for length := range 9 {
		for bits := range 1 << length {
			var input strings.Builder
			for i := range length {
				if bits&(1<<i) == 0 {
					input.WriteByte('a')
				} else {
					input.WriteByte('b')
				}
			}
			value := input.String()
			want := referenceMask(value, values)
			if got := r.Redact(value); got != want {
				t.Fatalf("complete %q: got %q, want %q", value, got, want)
			}
			assertAllStreamCuts(t, r, value, want)
		}
	}
}

func referenceMask(input string, secrets []string) string {
	covered := make([]bool, len(input))
	markers := make([]bool, len(input))
	markerEnd := 0
	for i := range len(input) {
		longest := referenceLongestMatch(input[i:], secrets)
		if longest == 0 {
			continue
		}
		for j := i; j < i+longest; j++ {
			covered[j] = true
		}
		if i >= markerEnd {
			markers[i] = true
			markerEnd = i + longest
		}
	}
	var output strings.Builder
	for i := range len(input) {
		if markers[i] {
			output.WriteString(defaultMask)
		}
		if !covered[i] {
			output.WriteByte(input[i])
		}
	}
	return output.String()
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

func TestStreamFormatCredentialsEverySplit(t *testing.T) {
	r := New(Options{})
	for _, test := range []struct{ input, want string }{
		{"clone https://user:pass@example.com/repo done", "clone https://***@example.com/repo done"},
		{"Authorization: Bearer api-token, safe", "Authorization: Bearer ***, safe"},
		{"before authorization = Basic\tcredential after", "before authorization = Basic\t*** after"},
		{"Authorization: Ba\u017fic opaque-token, safe", "Authorization: Ba\u017fic ***, safe"},
		{"clone http\u017f://user:pass@example.com/repo done", "clone http\u017f://***@example.com/repo done"},
		{"Authorization: Bearer a", "Authorization: Bearer ***"},
		{"https://example.com/path and ordinary output", "https://example.com/path and ordinary output"},
		{
			"first Authorization: Bearer token next https://user:pass@example.com/end",
			"first Authorization: Bearer *** next https://***@example.com/end",
		},
	} {
		if got := r.Redact(test.input); got != test.want {
			t.Fatalf("complete %q: got %q, want %q", test.input, got, test.want)
		}
		assertAllStreamCuts(t, r, test.input, test.want)
	}
}

func TestStreamAuthorizationRecoversOverlappingNames(t *testing.T) {
	r := New(Options{})
	for _, test := range []struct{ input, want string }{
		{
			"authorizAuthorization: Bearer canary-token, safe",
			"authorizAuthorization: Bearer ***, safe",
		},
		{
			"Authorization: BAuthorization: Basic nested-token, safe",
			"Authorization: BAuthorization: Basic ***, safe",
		},
		{
			"Authorization: BaAuthorization: Bearer nested-token, safe",
			"Authorization: BaAuthorization: Bearer ***, safe",
		},
		{
			"Authorization   Authorization: Bearer another-token, safe",
			"Authorization   Authorization: Bearer ***, safe",
		},
		{
			"Authorization: Bearer abcAuthorization:def, safe",
			"Authorization: Bearer ***, safe",
		},
		{
			"Authorization: Bearer abcAuthorization:def, Authorization: Basic other-token",
			"Authorization: Bearer ***, Authorization: Basic ***",
		},
	} {
		if got := r.Redact(test.input); got != test.want {
			t.Fatalf("complete %q = %q, want %q", test.input, got, test.want)
		}
		assertAllStreamCuts(t, r, test.input, test.want)
	}
}

func TestStreamCustomMaskAndIntersectingSecrets(t *testing.T) {
	r := New(Options{
		Values: []string{testShortSecret, testCrossingSecret, testCrossingTail},
		Mask:   testProviderMask,
	})
	input := "safe abc-secret-part-tail Authorization: Bearer api-token, visible"
	want := "safe [REDACTED] Authorization: Bearer [REDACTED], visible"
	if got := r.Redact(input); got != want {
		t.Fatalf("complete redaction = %q, want %q", got, want)
	}
	assertAllStreamCuts(t, r, input, want)
}

func TestCredentialRecognitionUsesOriginalInput(t *testing.T) {
	tests := []struct {
		name, secret, input, want string
	}{
		{
			"authorization scheme",
			"Bearer",
			"before Authorization: Bearer unknown-token after",
			"before Authorization: *** *** after",
		},
		{
			"authorization spanning",
			"Bearer unknown-token",
			"before Authorization: Bearer unknown-token after",
			"before Authorization: *** after",
		},
		{
			"url scheme",
			testURLScheme,
			"before https://user:unknown-password@example.com after",
			"before ******@example.com after",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := New(Options{Values: []string{test.secret}})
			if got := r.Redact(test.input); got != test.want {
				t.Fatalf("complete = %q, want %q", got, test.want)
			}
			assertAllStreamCuts(t, r, test.input, test.want)
		})
	}
}

func assertAllStreamCuts(t *testing.T, r *Redactor, input, want string) {
	t.Helper()
	for split := 0; split <= len(input); split++ {
		stream := NewStream(r)
		got := stream.RedactChunk(
			input[:split],
		) + stream.RedactChunk(
			input[split:],
		) + stream.Flush()
		if got != want {
			t.Fatalf("input %q split %d: got %q, want %q", input, split, got, want)
		}
	}
	stream := NewStream(r)
	var output strings.Builder
	for i := range len(input) {
		output.WriteString(stream.RedactChunk(input[i : i+1]))
	}
	output.WriteString(stream.Flush())
	if got := output.String(); got != want {
		t.Fatalf("input %q byte chunks: got %q, want %q", input, got, want)
	}
}

func TestStreamBoundedRepeatedKnownSecret(t *testing.T) {
	const count = 1 << 14
	input := strings.Repeat(testRepeatedSecret, count)
	r := New(Options{Values: []string{testRepeatedSecret}})
	stream := NewStream(r)
	emitted := stream.RedactChunk(input)
	if len(emitted) < 3*(count-1) {
		t.Fatalf("repeated output retained: emitted %d bytes", len(emitted))
	}
	if len(stream.pending) > max(r.maxLength-1, formatCarryLimit) {
		t.Fatalf("unbounded carry: %d", len(stream.pending))
	}
	if got, want := emitted+stream.Flush(), r.Redact(input); got != want {
		t.Fatal("stream and complete redaction differ")
	}
}

func TestStreamLongAuthorizationAndOrdinaryURL(t *testing.T) {
	r := New(Options{})
	stream := NewStream(r)
	var output strings.Builder
	output.WriteString(stream.RedactChunk("before Authorization: Bearer "))
	for range 1024 {
		output.WriteString(stream.RedactChunk("secret-token"))
		if len(stream.pending) > formatCarryLimit {
			t.Fatal("authorization carry grew")
		}
	}
	output.WriteString(stream.RedactChunk(", after"))
	output.WriteString(stream.Flush())
	if got, want := output.String(), "before Authorization: Bearer ***, after"; got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
	ordinary := testURLScheme + strings.Repeat("a", 1024) + "/path"
	stream = NewStream(r)
	got := stream.RedactChunk(ordinary[:600]) + stream.RedactChunk(ordinary[600:]) + stream.Flush()
	if got != "https://***/path" {
		t.Fatalf("overflowed ordinary URL = %q", got)
	}
	stream = NewStream(r)
	if got := stream.RedactChunk(ordinary) + stream.Flush(); got != ordinary {
		t.Fatal("fully resolved ordinary URL changed")
	}
}

func TestStreamURLOverflowMasksUntilDelimiter(t *testing.T) {
	scheme := testURLScheme
	authority := testURLUser + strings.Repeat("x", 600)
	tests := []struct {
		name, tail, want string
	}{
		{"userinfo", testURLSuffix, scheme + "***@example.test/x"},
		{"path", "/path", scheme + "***/path"},
		{"query", "?u=@example.test/x", scheme + "***?u=@example.test/x"},
		{"fragment", "#u=@example.test/x", scheme + "***#u=@example.test/x"},
		{"space", " done", scheme + "*** done"},
		{"eof", "", scheme + defaultMask},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := NewStream(New(Options{Values: []string{testURLUser}}))
			first := stream.RedactChunk(scheme + authority)
			got := first + stream.RedactChunk(test.tail) + stream.Flush()
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
			if strings.Contains(got, authority) || strings.Count(got, defaultMask) != 1 {
				t.Fatalf("unmasked authority or duplicate marker: %q", got)
			}
		})
	}
}

func TestStreamURLOverflowChunkBoundaries(t *testing.T) {
	input := testURLScheme + testURLUser + strings.Repeat("x", 1100) + testURLSuffix
	want := "https://***@example.test/x"
	for _, chunkSize := range []int{1, 7, 128, 511, 512, 513, len(input)} {
		stream := NewStream(New(Options{}))
		var output strings.Builder
		for i := 0; i < len(input); i += chunkSize {
			output.WriteString(stream.RedactChunk(input[i:min(i+chunkSize, len(input))]))
			if len(stream.pending) > formatCarryLimit {
				t.Fatalf("chunk %d retained %d bytes", chunkSize, len(stream.pending))
			}
		}
		output.WriteString(stream.Flush())
		if got := output.String(); got != want {
			t.Fatalf("chunk %d: got %q, want %q", chunkSize, got, want)
		}
	}
}

func TestStreamURLQueryAndFragmentBoundaries(t *testing.T) {
	r := New(Options{})
	for _, suffix := range []string{"?next=@example.test/x", "#next=@example.test/x"} {
		short := testURLScheme + "example.test" + suffix
		if got := r.Redact(short); got != short {
			t.Fatalf("complete ordinary URL = %q, want %q", got, short)
		}
		assertAllStreamCuts(t, r, short, short)
		long := testURLScheme + strings.Repeat("a", 600) + suffix + "\n"
		stream := NewStream(r)
		if got := stream.RedactChunk(long); got != long {
			t.Fatalf(
				"resolved long ordinary URL changed: got %d bytes, want %d",
				len(got),
				len(long),
			)
		}
		if got := stream.Flush(); got != "" {
			t.Fatalf("unexpected trailing output = %q", got)
		}
	}
}

func TestStreamEncodedURLUserinfoAcrossSplits(t *testing.T) {
	r := New(Options{})
	input := testURLScheme + "user%3Fpart%23token@example.test/x"
	want := testURLScheme + "***@example.test/x"
	if got := r.Redact(input); got != want {
		t.Fatalf("complete encoded userinfo = %q, want %q", got, want)
	}
	assertAllStreamCuts(t, r, input, want)
	stream := NewStream(r)
	first := testURLScheme + "user%3F" + strings.Repeat("x", 600)
	got := stream.RedactChunk(
		first,
	) + stream.RedactChunk(
		"%23suffix@example.test/x",
	) + stream.Flush()
	if got != want {
		t.Fatalf("overflow encoded userinfo = %q, want %q", got, want)
	}
}

func TestStreamURLLookaheadThreshold(t *testing.T) {
	scheme := testURLScheme
	short := scheme + strings.Repeat("x", formatCarryLimit-len(scheme))
	stream := NewStream(New(Options{}))
	if got := stream.RedactChunk(short); got != "" {
		t.Fatalf("boundary emitted prematurely: %q", got)
	}
	if got := stream.RedactChunk(
		testURLSuffix,
	) + stream.Flush(); got != scheme+"***@example.test/x" {
		t.Fatalf("boundary credential = %q", got)
	}
	stream = NewStream(New(Options{}))
	if got := stream.RedactChunk(short + "x"); got != scheme+defaultMask {
		t.Fatalf("overflow did not mask promptly: %q", got)
	}
	if got := stream.RedactChunk(testURLSuffix) + stream.Flush(); got != testURLSuffix {
		t.Fatalf("overflow continuation = %q", got)
	}
	stream = NewStream(New(Options{}))
	ordinary := scheme + "example.test"
	if got := stream.RedactChunk(
		ordinary,
	) + stream.RedactChunk(
		"/x",
	) + stream.Flush(); got != ordinary+"/x" {
		t.Fatalf("short ordinary URL = %q", got)
	}
}

func TestStreamShortUnresolvedURLAtEOF(t *testing.T) {
	stream := NewStream(New(Options{}))
	value := testURLScheme + "example.test"
	if got := stream.RedactChunk(value); got != "" {
		t.Fatalf("unresolved URL emitted early: %q", got)
	}
	if got := stream.Flush(); got != value {
		t.Fatalf("unresolved URL at EOF = %q, want %q", got, value)
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("second flush = %q", got)
	}
}

func TestStreamURLAuthorityInsideAuthorizationToken(t *testing.T) {
	stream := NewStream(New(Options{}))
	first := "Authorization: Bearer https://user,"
	second := strings.Repeat("x", 600) + "@example.test/x done"
	got := stream.RedactChunk(first) + stream.RedactChunk(second) + stream.Flush()
	want := "Authorization: Bearer ***@example.test/x done"
	if got != want {
		t.Fatalf("nested URL = %q, want %q", got, want)
	}
	stream = NewStream(New(Options{}))
	got = stream.RedactChunk(first) + stream.RedactChunk(strings.Repeat("x", 600)) +
		stream.RedactChunk("@example.test/x done") + stream.Flush()
	if got != want {
		t.Fatalf("overflowed nested URL = %q, want %q", got, want)
	}
}

func TestStreamURLOverflowWithLongExplicitPrefix(t *testing.T) {
	scheme := testURLScheme
	secret := scheme + testURLUser + strings.Repeat("x", 700) + "last"
	stream := NewStream(New(Options{Values: []string{secret}}))
	first := scheme + testURLUser + strings.Repeat("x", 600)
	if got := stream.RedactChunk(first); got != "" {
		t.Fatalf("explicit prefix leaked = %q", got)
	}
	got := stream.RedactChunk(strings.Repeat("x", 100)+"last@example.test/x") + stream.Flush()
	if got != "***@example.test/x" {
		t.Fatalf("explicit credential = %q", got)
	}
}

func TestStreamAuthorizationLongWhitespace(t *testing.T) {
	spaces := strings.Repeat(" ", 1100)
	input := "before Authorization" + spaces + ":" + spaces + "Bearer" + spaces + "unknown-token, after"
	r := New(Options{Values: []string{"Bearer"}})
	want := "before Authorization" + spaces + ":" + spaces + "***" + spaces + "***, after"
	if got := r.Redact(input); got != want {
		t.Fatal("complete authorization masking differs")
	}
	for _, chunkSize := range []int{1, 17, 511, 1024} {
		stream := NewStream(r)
		var output strings.Builder
		for i := 0; i < len(input); i += chunkSize {
			end := min(i+chunkSize, len(input))
			output.WriteString(stream.RedactChunk(input[i:end]))
			if len(stream.pending) > max(r.maxLength-1, formatCarryLimit) {
				t.Fatalf("chunk %d retained %d bytes", chunkSize, len(stream.pending))
			}
		}
		output.WriteString(stream.Flush())
		if got := output.String(); got != want {
			t.Fatalf("chunk %d: authorization output differs", chunkSize)
		}
	}
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

func TestStreamLongKnownPrefixChunks(t *testing.T) {
	prefix := strings.Repeat("x", 4096)
	r := New(Options{Values: []string{prefix + "Z"}})
	tests := []struct {
		name, input, want string
	}{
		{benchComplete, prefix + "Z", defaultMask},
		{benchMismatch, prefix + "Q", prefix + "Q"},
		{benchFlush, prefix, prefix},
		{benchSliding, prefix + prefix, prefix + prefix},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, chunks := range [][]int{{1}, {1, 7, 3, 13}} {
				stream := NewStream(r)
				got, peak := streamChunks(stream, test.input, chunks)
				if got != test.want {
					t.Fatalf("chunks %v: output differs", chunks)
				}
				if peak > streamPendingBound(r) {
					t.Fatalf("chunks %v: retained %d bytes", chunks, peak)
				}
			}
		})
	}
}

func TestStreamDenseAndFormatSliding(t *testing.T) {
	prefix := strings.Repeat("x", 4096)
	formatPrefix := strings.Repeat(testFormatPart, 820)
	tests := []struct {
		name   string
		values []string
		input  string
	}{
		{benchDense, []string{prefix + "Z", testDenseToken}, prefix + prefix},
		{benchFormat, []string{formatPrefix + "Z"}, formatPrefix + formatPrefix},
		{
			benchURL,
			[]string{formatPrefix + "Z"},
			testURLScheme + formatPrefix + formatPrefix + testURLSuffix,
		},
		{
			benchAuthURL,
			[]string{formatPrefix + "Z"},
			"Authorization: Bearer " + testURLScheme + testURLUser + "," + formatPrefix + formatPrefix + testURLSuffix,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := New(Options{Values: test.values})
			want := r.Redact(test.input)
			for _, chunks := range [][]int{{1}, {1, 7, 3, 13}} {
				stream := NewStream(r)
				got, peak := streamChunks(stream, test.input, chunks)
				if got != want {
					t.Fatalf("chunks %v: output differs", chunks)
				}
				if peak > streamPendingBound(r) {
					t.Fatalf("chunks %v: retained %d bytes", chunks, peak)
				}
			}
		})
	}
}

func TestStreamDefersDenseAndFormatChurn(t *testing.T) {
	prefix := strings.Repeat(testFormatPart, 820)
	r := New(Options{Values: []string{prefix + "Z", testDenseToken}})
	stream := NewStream(r)
	if got := stream.RedactChunk(prefix); got != "" {
		t.Fatalf("proper prefix emitted: %q", got)
	}
	if got := stream.RedactChunk(prefix[:100]); got != "" {
		t.Fatalf("small churn emitted: %q", got)
	}
	if len(stream.pending) <= r.maxLength {
		t.Fatalf("small churn was not retained: %d", len(stream.pending))
	}
	if got := stream.RedactChunk(""); got != "" {
		t.Fatalf("empty chunk emitted: %q", got)
	}
	input := prefix + prefix[:100]
	if got, want := stream.Flush(), r.Redact(input); got != want {
		t.Fatalf("flushed output differs: got %d bytes, want %d", len(got), len(want))
	}
	if got := stream.Flush(); got != "" {
		t.Fatalf("repeated flush emitted: %q", got)
	}
}

func streamPendingBound(r *Redactor) int {
	known := max(0, r.maxLength-1)
	return max(known+known/2, max(known, formatCarryLimit)+len("http\u017f://"))
}

func TestStreamSlidingPreservesLaterCredentials(t *testing.T) {
	prefix := strings.Repeat("x", 64)
	r := New(Options{Values: []string{prefix + "Z"}})
	for _, suffix := range []string{
		"xxxxxxZ end",
		"Authorization: Bearer unknown-token, safe",
		"authorization = Basic\tunknown-token done",
		testCredentialURL() + "/path",
	} {
		input := prefix + suffix
		want := r.Redact(input)
		for _, chunks := range [][]int{{1}, {1, 7, 3, 13}} {
			got, _ := streamChunks(NewStream(r), input, chunks)
			if got != want {
				t.Fatalf("suffix %q chunks %v: got %q, want %q", suffix, chunks, got, want)
			}
		}
	}
}

func TestStreamKnownPrefixOverlapsURLAndAuthorization(t *testing.T) {
	authority := testURLUser + strings.Repeat("x", 600)
	secret := testURLScheme + authority + "Z"
	r := New(Options{Values: []string{secret}})
	for _, input := range []string{
		secret + testURLSuffix,
		testURLScheme + authority + testURLSuffix,
		"Authorization: Bearer " + secret + " after",
	} {
		for _, chunks := range [][]int{{1}, {1, 7, 3, 13}} {
			stream := NewStream(r)
			got, _ := streamChunks(stream, input, chunks)
			if want := r.Redact(input); got != want {
				t.Fatalf("chunks %v: got %q, want %q", chunks, got, want)
			}
			if strings.Contains(got, authority) {
				t.Fatal("authority leaked")
			}
		}
	}
}

func streamChunks(stream *Stream, input string, chunks []int) (string, int) {
	var output strings.Builder
	peak := 0
	for i, part := 0, 0; i < len(input); part++ {
		end := min(i+chunks[part%len(chunks)], len(input))
		output.WriteString(stream.RedactChunk(input[i:end]))
		peak = max(peak, len(stream.pending))
		i = end
	}
	output.WriteString(stream.Flush())
	return output.String(), peak
}

func BenchmarkStreamKnownPrefix(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{
		{"4KiB", 4096},
		{"64KiB", 65536},
	} {
		b.Run(size.name, func(b *testing.B) {
			for _, chunks := range []struct {
				name string
				step []int
			}{
				{"tiny", []int{1}},
				{"irregular", []int{1, 7, 3, 13}},
			} {
				b.Run(chunks.name, func(b *testing.B) {
					for _, mode := range []string{
						benchComplete, benchMismatch, benchFlush, benchSliding, benchDense, benchFormat, benchURL, benchAuthURL,
					} {
						b.Run(mode, func(b *testing.B) {
							benchmarkStreamKnownPrefix(b, size.n, chunks.step, mode)
						})
					}
				})
			}
		})
	}
}

func benchmarkStreamKnownPrefix(b *testing.B, size int, chunks []int, mode string) {
	prefix := strings.Repeat("x", size)
	if mode == benchFormat || mode == benchURL || mode == benchAuthURL {
		prefix = strings.Repeat(testFormatPart, size/len(testFormatPart))
	}
	values := []string{prefix + "Z"}
	if mode == benchDense {
		values = append(values, testDenseToken)
	}
	r := New(Options{Values: values})
	input, want := benchmarkInput(prefix, mode, r)
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		got, _ := streamChunks(NewStream(r), input, chunks)
		if got != want {
			b.Fatal("stream output differs")
		}
	}
}

func benchmarkInput(prefix, mode string, r *Redactor) (string, string) {
	switch mode {
	case benchComplete:
		return prefix + "Z", defaultMask
	case benchMismatch:
		return prefix + "Q", prefix + "Q"
	case benchSliding, benchDense, benchFormat:
		input := prefix + prefix
		return input, r.Redact(input)
	case benchURL:
		input := testURLScheme + prefix + prefix + testURLSuffix
		return input, r.Redact(input)
	case benchAuthURL:
		input := "Authorization: Bearer " + testURLScheme + testURLUser + "," + prefix + prefix + testURLSuffix
		return input, r.Redact(input)
	default:
		return prefix, prefix
	}
}

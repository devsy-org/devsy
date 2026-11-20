package redact

import (
	"strings"
	"testing"
)

const (
	testShortSecret    = "abc"
	testSelfOverlap    = "aba"
	testOverlapReverse = "bab"
	testCrossingSecret = "abc-secret-part"
	testCrossingTail   = "part-tail"
	testRepeatedSecret = "abcabc"
	testBearer         = "Bearer"
	testBearerValue    = "Bearer fresh-token"
	testOrdinary       = "ordinary"
	testMaskedBearer   = testBearer + " " + testProviderMask
	testProviderMask   = "[REDACTED]"
)

func testCredentialURL() string {
	return strings.Join([]string{"https://user", "pass@example.com"}, ":")
}

func TestRedactValuesAndOverlap(t *testing.T) {
	tests := []struct {
		name, input, want string
		values            []string
	}{
		{"contained", "a secret b", "a *** b", []string{"sec", "secret"}},
		{
			"crossing",
			"safe abc-secret-part-tail visible",
			"safe [REDACTED] visible",
			[]string{testShortSecret, testCrossingSecret, testCrossingTail},
		},
		{"self overlap", "x ababa y", "x *** y", []string{testSelfOverlap}},
		{"repeated", "abcabcabcabc", "******", []string{testRepeatedSecret}},
		{"binary", "a\x00\xffba\x00\xffb", "******", []string{"a\x00\xffb"}},
		{testOrdinary, "ordinary output", "ordinary output", []string{"private"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mask := ""
			if test.name == "crossing" {
				mask = testProviderMask
			}
			r := New(Options{Values: test.values, Mask: mask})
			if got := r.Redact(test.input); got != test.want {
				t.Fatalf("Redact = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEnvironmentAndExplicitPolicy(t *testing.T) {
	opts := Options{
		Values: []string{testShortSecret, "whole-secret\nline-secret\nxyz"},
		Environment: []string{
			"PATH=/usr/local/bin", "API_TOKEN=canary-token", "COMMAND=secret-command", "AUTH=q",
		},
		AdditionalEnvironmentKeys: []string{"command"},
		Mask:                      testProviderMask, MinExplicitBytes: 4, SplitExplicitLines: true,
	}
	r := New(opts)
	opts.Values[0] = testOrdinary
	opts.Environment[1] = "API_TOKEN=changed"
	input := "abc xyz whole-secret line-secret /usr/local/bin canary-token secret-command q"
	want := "abc xyz [REDACTED] [REDACTED] /usr/local/bin [REDACTED] [REDACTED] [REDACTED]"
	if got := r.Redact(input); got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}

func TestAdditionalEnvironmentKeysAreExact(t *testing.T) {
	r := New(Options{
		Environment: []string{
			"command=hidden-command",
			"COMMANDLINE=visible-commandline",
			"PRIVATE_KEY=hidden-private",
			"NORMAL=visible-normal",
		},
		AdditionalEnvironmentKeys: []string{"COMMAND"},
	})
	input := "hidden-command visible-commandline hidden-private visible-normal"
	want := "*** visible-commandline *** visible-normal"
	if got := r.Redact(input); got != want {
		t.Fatalf("environment Redact = %q, want %q", got, want)
	}
}

func TestEntriesAndCombine(t *testing.T) {
	r := Combine(FromEntries([]string{"A=alpha=secret", "empty=", "invalid"}), nil,
		New(Options{Values: []string{"beta-secret"}, Mask: testProviderMask}))
	if got := r.Redact("alpha=secret beta-secret"); got != "*** ***" {
		t.Fatalf("combined Redact = %q", got)
	}
}

func TestFormatCredentialsAndNil(t *testing.T) {
	var r *Redactor
	input := testCredentialURL() + " Authorization: Bearer api-token, Basic"
	want := "https://***@example.com Authorization: ***"
	if got := r.Redact(input); got != want {
		t.Fatalf("nil Redact = %q, want %q", got, want)
	}
	long := "https://" + strings.Repeat("x", 1024) + "@example.com"
	if got := r.Redact(long); got != "https://***@example.com" {
		t.Fatalf("long complete URL leaked: %q", got)
	}
}

func TestRedactFieldAuthorization(t *testing.T) {
	r := New(Options{Values: []string{"known-secret"}, Mask: testProviderMask})
	for _, test := range []struct {
		key, value, want string
	}{
		{authorizationField, testBearerValue, testMaskedBearer},
		{"authorization", "  Basic\tfresh-token, safe", "  Basic\t[REDACTED]"},
		{"AUTHORIZATION", "Bearer known-secret", testMaskedBearer},
		{authorizationField, "Bearer " + testCredentialURL(), testMaskedBearer},
		{authorizationField, "prefix Authorization: Basic fresh-token", testProviderMask},
		{authorizationField, "Digest example", testProviderMask},
		{authorizationField, "custom example", testProviderMask},
		{authorizationField, " \t", " \t"},
		{
			authorizationField,
			"Bearer first, Authorization: Basic second",
			"Bearer [REDACTED]",
		},
		{testOrdinary, "Authorization: Bearer fresh-token", "Authorization: [REDACTED]"},
		{"X-Authorization", testBearerValue, testBearerValue},
		{authorizationField, testBearer, testBearer},
		{authorizationField, "Bearer ", "Bearer "},
	} {
		if got := r.RedactField(test.key, test.value); got != test.want {
			t.Fatalf("key %q value %q: got %q, want %q", test.key, test.value, got, test.want)
		}
	}
	var nilRedactor *Redactor
	if got := nilRedactor.RedactField(authorizationField, "Basic fresh-token"); got != "Basic ***" {
		t.Fatalf("nil field redaction = %q", got)
	}
}

func TestRedactFieldUsesOriginalSpans(t *testing.T) {
	r := New(Options{Values: []string{testBearer}})
	if got := r.RedactField(authorizationField, testBearerValue); got != "*** ***" {
		t.Fatalf("overlapping authorization field = %q", got)
	}
	if got := r.RedactField(testOrdinary, testBearerValue); got != "*** fresh-token" {
		t.Fatalf("ordinary field = %q", got)
	}
	whole := New(Options{Values: []string{testBearerValue}})
	if got := whole.RedactField(authorizationField, testBearerValue); got != defaultMask {
		t.Fatalf("whole value overlap = %q", got)
	}
}

func TestMatcherLongestAndProperSuffix(t *testing.T) {
	tests := []struct {
		values []string
		input  string
	}{
		{[]string{"a", "aba", testOverlapReverse, "abcabc"}, "abababcabcab"},
		{[]string{"a\x00\xffb", "\x00\xff", "\xffb"}, "a\x00\xffba\x00\xff"},
		{[]string{"line\nsecret", "line"}, "safe line\nsec"},
	}
	for _, test := range tests {
		r := New(Options{Values: test.values})
		assertMatcherLengths(t, r, test.input, test.values)
		assertMatcherBoundaries(t, r, test.input, test.values)
	}
}

func assertMatcherLengths(t *testing.T, r *Redactor, input string, values []string) {
	t.Helper()
	for cut := 0; cut <= len(input); cut++ {
		lengths := r.matchLengths(input, cut)
		for i := range cut {
			got := 0
			if i < len(lengths) {
				got = lengths[i]
			}
			if want := referenceLongestMatch(input[i:], values); got != want {
				t.Fatalf("input %q cut %d index %d: got %d, want %d", input, cut, i, got, want)
			}
		}
	}
}

func assertMatcherBoundaries(t *testing.T, r *Redactor, value string, values []string) {
	t.Helper()
	for end := 0; end <= len(value); end++ {
		input := value[:end]
		want := len(input)
		for i := range len(input) {
			for _, secret := range values {
				if len(input)-i < len(secret) && strings.HasPrefix(secret, input[i:]) {
					want = min(want, i)
				}
			}
		}
		if got := r.secretPrefixBoundary(input); got != want {
			t.Fatalf("input %q boundary = %d, want %d", input, got, want)
		}
	}
}

func TestMatcherLongSharedPrefixAndNoMatchAllocations(t *testing.T) {
	secret := strings.Repeat("a", 2048) + "b"
	r := New(Options{Values: []string{secret}})
	prefix := strings.Repeat("a", 32768) + "c "
	input := prefix + secret + " end"
	if got, want := r.Redact(input), prefix+"*** end"; got != want {
		t.Fatal("long shared-prefix redaction differed")
	}
	noMatch := strings.Repeat("a", 32768) + "c"
	if got := r.matchLengths(noMatch, len(noMatch)); got != nil {
		t.Fatal("no-match scan allocated a result")
	}
	if allocs := testing.AllocsPerRun(10, func() {
		_ = r.matchLengths(noMatch, len(noMatch))
	}); allocs != 0 {
		t.Fatalf("no-match matcher allocations = %v, want zero", allocs)
	}
	stream := NewStream(r)
	split := len(input) - len(secret) + 1024
	got := stream.RedactChunk(input[:split]) + stream.RedactChunk(input[split:]) + stream.Flush()
	if got != prefix+"*** end" {
		t.Fatal("long shared-prefix stream differed")
	}
}

func BenchmarkRedactSharedPrefix(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{
		{name: "4KiB", n: 4096},
		{name: "128KiB", n: 131072},
	} {
		b.Run(size.name, func(b *testing.B) {
			prefix := strings.Repeat("a", size.n)
			r := New(Options{Values: []string{prefix + "b"}})
			input := prefix + "c"
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = r.Redact(input)
			}
		})
	}
}

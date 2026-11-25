package recordmask

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
)

const canary = "CANARY"

var lineEndings = map[string]string{"unix": "\n", "dos": "\r\n", "mac": "\r"}

func maskWith(redactor *redact.Redactor, chunks []string) string {
	var out strings.Builder
	masker := New(redactor, func(text string) { out.WriteString(text) })
	for _, chunk := range chunks {
		masker.Write(chunk)
	}
	masker.Finish()
	return out.String()
}

func chunkings(input string, seed uint64) map[string][]string {
	all := map[string][]string{"all": {input}, "bytes": splitEvery(input, 1)}
	if len(input) <= 400 {
		for i := 1; i < len(input); i++ {
			all["split"+string(rune('A'+i%26))+strings.Repeat("'", i/26)] = []string{
				input[:i],
				input[i:],
			}
		}
	}
	for _, at := range []int{
		streamLineLimit - 1, streamLineLimit, streamLineLimit + 1, streamLineLimit + 2,
		maxJSONRecordBytes - 1, maxJSONRecordBytes, maxJSONRecordBytes + 1, maxJSONRecordBytes + 2,
		maxJSONRecordBytes + 7, maxJSONRecordBytes + 8, maxJSONRecordBytes + 9, maxJSONRecordBytes + 10,
	} {
		if at < len(input) {
			all["cap"+strconv.Itoa(at)] = []string{input[:at], input[at:]}
		}
	}
	state := seed
	randoms := 30
	if len(input) > 400 {
		randoms = 4
	}
	for n := range randoms {
		var chunks []string
		for rest := input; rest != ""; {
			state = state*6364136223846793005 + 1442695040888963407
			cut := min(len(rest), 1+int(state>>60)%9)
			chunks, rest = append(chunks, rest[:cut]), rest[cut:]
		}
		all["random"+string(rune('a'+n))] = chunks
	}
	return all
}

// maskedForAnyChunking returns the output after checking that every way of
// splitting the input into Write calls gives the same text.
func maskedForAnyChunking(t *testing.T, redactor *redact.Redactor, input string) string {
	t.Helper()
	want := maskWith(redactor, []string{input})
	for name, chunks := range chunkings(input, uint64(1)) {
		if got := maskWith(redactor, chunks); got != want {
			t.Fatalf("%s: output depends on chunking\nwhole: %q\n got:  %q", name, want, got)
		}
	}
	return want
}

func assertMaskedForAnyChunking(t *testing.T, input, want string) {
	t.Helper()
	assert.Equal(t, want, maskedForAnyChunking(t, newRedactor(), input))
}

func newRedactor() *redact.Redactor { return redact.New(redact.Options{}) }

func TestLineMaskFailClosedShapes(t *testing.T) {
	esc := "\x1b"
	auth := "Authorization: Bearer PLACEHOLDER"
	enc := "{\\u0022Cookie\\u0022:\\u0022" + canary + "\\u0022}"
	cases := map[string]string{
		"authorization fold LF":          auth + "\n {\"public\": !}\n " + canary + "\npublic tail\n",
		"authorization fold CRLF":        auth + "\r\n {\"public\": !}\r\n " + canary + "\r\npublic tail\r\n",
		"authorization fold record LF":   auth + "\n {\"public\": 1}\n " + canary + "\npublic tail\n",
		"authorization fold record CRLF": auth + "\r\n {\"public\": 1}\r\n " + canary + "\r\npublic tail\r\n",
		"escape then encoded quotes":     esc + "[2, \"" + enc + "\"]\npublic tail\n",
		"array with encoded quotes":      "public [2, \"" + enc + "\",]\npublic tail\n",
		"prefixed record":                "public log {\"Cookie\":\"" + canary + "\"}\npublic tail\n",
		"nested scalar array":            "[note [\"\\u0022Cookie: " + canary + "\\u0022\"]] public suffix\npublic tail\n",
		"pretty after prefix":            "public log {\n  \"Cookie\": \"" + canary + "\"\n}\npublic tail\n",
		"two documents after prefix":     "public {\"a\":1} {\"Cookie\":\"" + canary + "\"}\npublic tail\n",
		"prefix then fold line":          "public log\n  {\"Cookie\":\"" + canary + "\"}\npublic tail\n",
		"bracket then unindented":        "public log {\n\"Cookie\": \"" + canary + "\"\n}\npublic tail\n",
		"bracket CR endings":             "public log {\r\"Cookie\": \"" + canary + "\"\rpublic tail\r",
		"unterminated final line":        "public tail\npublic [\"" + canary + "\"]",
		"invalid record at start":        "{\"Cookie\":\"" + canary + "\", bad}\npublic tail\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got := maskedForAnyChunking(t, newRedactor(), input)
			assert.NotContains(t, got, canary)
			assert.NotContains(t, got, "PLACEHOLDER")
			assert.Contains(t, got, newRedactor().Mask())
			if strings.Contains(input, "public tail") {
				assert.Contains(t, got, "public tail")
			}
		})
	}
}

func TestLineMaskUsageQueryIsOverMasked(t *testing.T) {
	got := maskedForAnyChunking(t, newRedactor(), "usage: --query [\"a\\\":b\"|\"c\"]\n")
	assert.Equal(t, newRedactor().Mask()+"\n", got)
}

func TestLineMaskKeepsLineBreaks(t *testing.T) {
	mask := newRedactor().Mask()
	got := maskedForAnyChunking(t, newRedactor(), "a {\"x\"\r\nb\r\n")
	assert.Equal(t, mask+"\r\nb\r\n", got)
	assert.Equal(t, mask, maskedForAnyChunking(t, newRedactor(), "a [\"x\"]"))
}

func TestLineMaskLeavesReadableLines(t *testing.T) {
	cases := map[string]string{
		"unclosed bracket":  "error: invalid flag [not-a-flag\nrun --help\nfor usage\n",
		"progress":          "Progress [1/3] building\n",
		"ansi color":        "\x1b[31mred text\x1b[0m\n\x1b[1;32mgreen\x1b[0m\n",
		"clock":             "[12:30] started\n",
		"empty brackets":    "[]\n{}\n",
		"go log":            "2026/01/02 15:04:05 INFO step done key=value other=1 count=3\n",
		"go map":            "got map[a:b c:d]\n",
		"brace then word":   "{ foo }\nfunc() {\n  return\n}\n",
		"brace at line end": "func() {\nreturn\n}\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, input, maskedForAnyChunking(t, newRedactor(), input))
		})
	}
}

func TestLineMaskKeepsRecordFieldMasking(t *testing.T) {
	got := maskedForAnyChunking(t, newRedactor(),
		"{\"Cookie\":\""+canary+"\"}\n{\"message\":\"public detail\"}\n")
	assert.NotContains(t, got, canary)
	assert.Contains(t, got, "public detail")
}

func TestLineMaskIndentedRecordKeepsFieldMasking(t *testing.T) {
	input := "public log\n  {\"Cookie\":\"" + canary + "\",\"message\":\"public detail\"}\npublic tail\n"
	got := maskedForAnyChunking(t, newRedactor(), input)
	assert.NotContains(t, got, canary)
	assert.Contains(t, got, "public log")
	assert.Contains(t, got, "public detail")
	assert.Contains(t, got, "public tail")
}

func TestLineMaskOverlongLine(t *testing.T) {
	long := "public " + strings.Repeat("x", maxJSONRecordBytes+10)
	mask := newRedactor().Mask()
	for name, input := range map[string]string{
		"no newline": long,
		"lf":         long + "\npublic tail\n",
		"windows":    long + "\r\npublic tail\r\n",
		"fold":       "head\n" + strings.Repeat(" "+strings.Repeat("y", 1000)+"\n", 70) + "public tail\n",
	} {
		t.Run(name, func(t *testing.T) {
			for _, size := range []int{len(input), 4096, 1, 32769} {
				got := maskWith(newRedactor(), splitEvery(input, size))
				assert.Less(t, len(got), 200, "size %d", size)
				assert.True(t, strings.HasPrefix(got, mask), "size %d", size)
				assert.Equal(
					t,
					strings.Contains(input, "public tail"),
					strings.Contains(got, "public tail"),
				)
			}
		})
	}
}

func TestLineMaskStillRedactsPlainLines(t *testing.T) {
	secret := strings.Repeat("test-value-", 4)
	token := strings.Repeat("placeholdertoken", 3)
	redactor := redact.New(redact.Options{Environment: []string{"DEVSY_TOKEN=" + secret}})
	input := "step one " + secret + " kept\nAuthorization: Bearer " + token +
		"\nvia https://user:urlplaceholder@example.invalid/x\npublic tail\n"
	got := maskedForAnyChunking(t, redactor, input)
	for _, hidden := range []string{secret, token, "urlplaceholder"} {
		assert.NotContains(t, got, hidden)
	}
	assert.Contains(t, got, "kept")
	assert.Contains(t, got, "public tail")
}

type filledSeed struct {
	text string
	fill int
}

// filledSeeds hold a \x01 that the fuzz body replaces with fill bytes, so long
// inputs stay short enough for the fuzzer to keep.
var filledSeeds = []filledSeed{
	{"Authorization: Bearer \x01\nBearer X\npublic tail\n", maxJSONRecordBytes},
	{"Authorization: Bearer \x01 [\"p\"]\r\nBearer X\r\npublic tail\r\n", 16384},
	{"Proxy-Authorization = Basic \x01\rBearer X\rpublic tail\r", maxJSONRecordBytes},
	{"{\"pad\":\"\x01\"}\n X\npublic tail\n", maxJSONRecordBytes},
	{
		"banner\n" + strings.Repeat(
			" ",
			80,
		) + "{\"pad\":\"\x01\",!\n\"message\":\"X\"}\npublic tail\n",
		65458,
	},
	{
		"banner\r\n" + strings.Repeat(
			"\t",
			80,
		) + "{\"pad\":\"\x01\",!\r\n\"message\":\"X\"}\r\npublic tail\r\n",
		65458,
	},
	{"{\"Cookie\":\"\x01\"}\nBearer X\npublic tail\n", 16372},
	{"{\"Cookie\":\"\x01\"}\r\n \tX\r\npublic tail\r\n", 16372},
	{"{\"p\":0} Authorization: Bearer \x01 [\"public\"]\rBearer X\rpublic tail\r", 16344},
	{"{\"Cookie\":\"Authorization: x\"} banner\n\tX\npublic tail\n", 0},
}

func addFuzzSeeds(f *testing.F) {
	for _, seed := range []string{
		"public log {\"Cookie\":\"X\"}\n",
		"Authorization: Bearer P\r\n {\"a\": !}\r\n X\r\n",
		"Authorization: Bearer P\n log {\"a\": !}\n [\"X\"]\npublic tail\n",
		"{\"a\":1} [\"X\"]\npublic tail\n",
		"{\"a\":1} [\"public\"] Authorization:\nBearer X\npublic tail\n",
		"{\"a\":1} [\"public\"] Authorization:\r\nBearer X\r\npublic tail\r\n",
		"{\"a\":1} [\"public\"] Authorization:\rBearer X\rpublic tail\r",
		"{\"a\":1} [\"public\"] Authorization= \t\nBearer X\npublic tail\n",
		"\x1b[2, \"{\\u0022a\\u0022}\"]\n",
		"[1, 2\npublic\n{\n\"a\"\n}\n",
		"a {\r\"b\" \r\n\t\\x\n[\n\n",
	} {
		f.Add(seed, uint64(1), -1, 0)
	}
	for _, seed := range filledSeeds {
		f.Add(seed.text, uint64(1), -1, seed.fill)
	}
	for pad := range 5 {
		for _, tail := range capTails {
			f.Add(tail, uint64(pad), pad, 0)
		}
	}
}

func fillSeed(input string, fill int) string {
	before, after, found := strings.Cut(input, "\x01")
	if !found {
		return input
	}
	return before + strings.Repeat("x", max(0, fill)%70000) + after
}

func FuzzMaskerChunking(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, input string, seed uint64, pad, fill int) {
		if len(input) > 2000 {
			t.Skip()
		}
		input = fillSeed(input, fill)
		if pad >= 0 && pad < 5 {
			input = strings.Repeat("p", maxJSONRecordBytes-2+pad) + input
		}
		redactor := newRedactor()
		want := maskWith(redactor, []string{input})
		for name, chunks := range chunkings(input, seed) {
			if got := maskWith(redactor, chunks); got != want {
				t.Fatalf("%s: output depends on chunking", name)
			}
		}
	})
}

var capTails = []string{
	"\n  {\"Cookie\":\"" + canary + "\"}\npublic tail\n",
	"\n{\"Cookie\":\"" + canary + "\"}\npublic tail\n",
	"\n  {\n    \"Cookie\": \"" + canary + "\"\n  }\npublic tail\n",
	"\n \t {\"Cookie\":\"" + canary + "\"}\r\n [\"x\"]\r\npublic tail\r\n",
	" {\n\"Cookie\":\"" + canary + "\"}\n  [\"x\"]\npublic tail\n",
}

func TestLineMaskLinesAroundTheLimit(t *testing.T) {
	for pad := range 5 {
		line := strings.Repeat("p", maxJSONRecordBytes-2+pad)
		for _, tail := range capTails {
			got := maskedForAnyChunking(t, newRedactor(), line+tail)
			assert.NotContains(t, got, canary, "pad %d tail %q", pad, tail)
			assert.Contains(t, got, "public tail", "pad %d tail %q", pad, tail)
		}
	}
}

func TestLineMaskBracketFoldAfterMaskedUnit(t *testing.T) {
	for name, eol := range map[string]string{"lf": "\n", "crlf": "\r\n", "cr": "\r"} {
		t.Run(name, func(t *testing.T) {
			input := "Authorization: Bearer PLACEHOLDER" + eol + " log {\"public\": !}" + eol +
				" [\"" + canary + "\"]" + eol + "public tail" + eol
			got := maskedForAnyChunking(t, newRedactor(), input)
			assert.Equal(t, newRedactor().Mask()+eol+"public tail"+eol, got)
		})
	}
}

func TestLineMaskSuffixAfterRecord(t *testing.T) {
	mask := newRedactor().Mask()
	cases := map[string]string{
		"array suffix":  "{\"a\":1} [\"" + canary + "\"]\npublic tail\n",
		"object suffix": "{\"a\":1} public {\"Cookie\":\"" + canary + "\"}\npublic tail\n",
		"adjacent":      "{\"a\":1}[\"" + canary + "\"]\npublic tail\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			got := maskedForAnyChunking(t, newRedactor(), input)
			assert.NotContains(t, got, canary)
			assert.Contains(t, got, mask)
			assert.Contains(t, got, "public tail")
		})
	}
}

func TestLineMaskSuffixAfterOversizedRecord(t *testing.T) {
	input := oversizedRecord("  \"Note\": \"x\"\n} [\""+canary+"\"]\n") + "public tail\n"
	for _, size := range []int{len(input), 4096, 7} {
		got := maskWith(newRedactor(), splitEvery(input, size))
		assert.NotContains(t, got, canary, "size %d", size)
		assert.True(t, strings.HasSuffix(got, "public tail\n"), "size %d", size)
	}
}

func TestLineMaskColonEndingMaskedUnitMasksNextLine(t *testing.T) {
	mask := newRedactor().Mask()
	record := Text(newRedactor(), "{\"a\":1}")
	long := strings.Repeat("x", maxJSONRecordBytes)
	for name, eol := range lineEndings {
		t.Run(name, func(t *testing.T) {
			tail := "Bearer " + canary + eol + "public tail" + eol
			assertMaskedForAnyChunking(t,
				"{\"a\":1} [\"public\"] Authorization:"+eol+tail,
				record+mask+eol+mask+eol+"public tail"+eol)
			assertMaskedForAnyChunking(t,
				" log {\"public\"} Authorization: \t"+eol+tail,
				mask+eol+mask+eol+"public tail"+eol)
			assertMaskedForAnyChunking(t,
				long+" {\"a\"} Authorization:"+eol+tail,
				mask+"\n"+mask+eol+"public tail"+eol)
			assertMaskedForAnyChunking(t, "log [\"a\"] Authorization:", mask)
		})
	}
}

func TestLineMaskEqualsEndingMaskedUnitMasksNextLine(t *testing.T) {
	mask := newRedactor().Mask()
	record := Text(newRedactor(), "{\"a\":1}")
	for name, eol := range lineEndings {
		t.Run(name, func(t *testing.T) {
			assertMaskedForAnyChunking(
				t,
				"{\"a\":1} [\"public\"] Authorization= \t"+eol+"Bearer "+canary+eol+"public tail"+eol,
				record+mask+eol+mask+eol+"public tail"+eol,
			)
		})
	}
}

func TestLineMaskIndentationCountsTowardsTheLimit(t *testing.T) {
	mask := newRedactor().Mask()
	for name, eol := range lineEndings {
		for _, indent := range []string{strings.Repeat(" ", 80), strings.Repeat("\t", 80)} {
			record := indent + "{\"pad\":\"" + strings.Repeat("x", 65458) +
				"\",!" + eol + "\"message\":\"" + canary + "\"}" + eol + "public tail" + eol
			t.Run(name, func(t *testing.T) {
				assertMaskedForAnyChunking(
					t,
					"banner"+eol+record,
					"banner"+eol+mask+"\npublic tail"+eol,
				)
				assertMaskedForAnyChunking(t, record, mask+"\npublic tail"+eol)
			})
		}
	}
}

func TestLineMaskLongHeaderLineMasksSchemeLine(t *testing.T) {
	mask := newRedactor().Mask()
	huge := strings.Repeat("x", maxJSONRecordBytes)
	for name, eol := range lineEndings {
		t.Run(name, func(t *testing.T) {
			next := "Bearer " + canary + eol + "public tail" + eol
			headers := []string{
				"Authorization: Bearer ",
				"Proxy-Authorization = Basic ",
				"Proxy-Authorization: Basic ",
			}
			for _, header := range headers {
				assertMaskedForAnyChunking(
					t,
					header+huge+eol+next,
					mask+"\n"+mask+eol+"public tail"+eol,
				)
			}
			small := strings.Repeat("x", streamLineLimit)
			assertMaskedForAnyChunking(t, "Authorization: Bearer "+small+" [\"public\"]"+eol+next,
				mask+eol+mask+eol+"public tail"+eol)
			assertMaskedForAnyChunking(t, "{\"pad\":\""+huge+"\"}"+eol+next,
				mask+"\n"+mask+eol+"public tail"+eol)
		})
	}
}

func TestLineMaskStopsAtTheStreamLineLimit(t *testing.T) {
	mask := newRedactor().Mask()
	const tail = " [\"p\"]"
	next := "\nBearer " + canary + "\npublic tail\n"
	for _, extra := range []int{0, 1} {
		plain := "a=" + strings.Repeat("x", streamLineLimit-len("a=")+extra)
		stream := diagnosticredact.Stream(newRedactor())
		seen := stream.RedactChunk(plain+next) + stream.Flush()
		assert.Equal(t, extra == 0, strings.Contains(seen, canary), "stream, extra %d", extra)

		line := plain[:len(plain)-len(tail)] + tail
		want := mask + next
		if extra == 1 {
			want = mask + "\n" + mask + "\npublic tail\n"
		}
		assert.Equal(t, want, maskedForAnyChunking(t, newRedactor(), line+next), "extra %d", extra)
	}
}

func TestLineMaskRecordPathKeepsFoldState(t *testing.T) {
	mask := newRedactor().Mask()
	round6 := "ROUND6_" + canary
	for name, eol := range lineEndings {
		t.Run(name, func(t *testing.T) {
			cookie := "{\"Cookie\":\"" + strings.Repeat("x", streamLineLimit-12) + "\"}"
			record := Text(newRedactor(), cookie)
			assertMaskedForAnyChunking(t, cookie+eol+"Bearer "+round6+eol+"public tail"+eol,
				record+eol+mask+eol+"public tail"+eol)
			assertMaskedForAnyChunking(t, cookie+eol+" \t"+round6+eol+"public tail"+eol,
				record+mask+eol+"public tail"+eol)

			wide := "{\"p\":0} Authorization: Bearer " + strings.Repeat(
				"x",
				streamLineLimit-40,
			) + " [\"public\"]"
			assertMaskedForAnyChunking(t, wide+eol+"Bearer "+round6+eol+"public tail"+eol,
				Text(newRedactor(), "{\"p\":0}")+mask+eol+mask+eol+"public tail"+eol)

			header := "{\"Cookie\":\"Authorization: placeholder\"} banner"
			input := header + eol + "\t" + round6 + eol + "public tail" + eol
			assertMaskedForAnyChunking(
				t,
				input,
				Text(
					newRedactor(),
					"{\"Cookie\":\"Authorization: placeholder\"}",
				)+mask+eol+"public tail"+eol,
			)
			assert.NotContains(t, ReportText(newRedactor(), input), round6)
		})
	}
}

func TestLineMaskHeaderPatternMatchesTheStream(t *testing.T) {
	for _, header := range []string{
		"Authorization: x", "proxy-authorization = y", "Proxy-Authorization:", "AUTHORIZATION\t=v",
		"Authorization x", "xAuthorization: z", "authorization", "Proxy Authorization: x", "Authorization\"=x",
	} {
		stream := diagnosticredact.Stream(newRedactor())
		seen := stream.RedactChunk("{} "+header+" banner\n\t"+canary+"\n") + stream.Flush()
		assert.Equal(t, !strings.Contains(seen, canary), headerStart.MatchString(header), header)
	}
}

func TestLineMaskOversizedRecordIsMaskPlusNewline(t *testing.T) {
	mask := newRedactor().Mask()
	long := strings.Repeat("x", maxJSONRecordBytes)
	for name, eol := range lineEndings {
		t.Run(name, func(t *testing.T) {
			want := mask + "\npublic tail" + eol
			assertMaskedForAnyChunking(t,
				"{\"pad\":\""+long+"\"}"+eol+" "+canary+eol+"public tail"+eol, want)
			assertMaskedForAnyChunking(t,
				"{\"pad\":\""+long+"\"}"+eol+" {\"a\":\""+canary+"\"}"+eol+"public tail"+eol, want)
			assertMaskedForAnyChunking(t,
				"{\"pad\":\""+long+"\",\n\"message\":\""+canary+"\",!}"+eol+"public tail"+eol, want)
		})
	}
}

func TestLineMaskLeadingBlanksPastTheLimit(t *testing.T) {
	mask := newRedactor().Mask()
	blanks := strings.Repeat(" ", maxJSONRecordBytes+1)
	assertMaskedForAnyChunking(t,
		blanks+"{\"x\":\""+canary+"\"}\npublic tail\n", mask+"\npublic tail\n")
}

func allocatedBytes(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A copy of the unread input per handoff grows with records times input, so
// doubling the input would quadruple the allocation.
func TestLineMaskAllocationIsLinear(t *testing.T) {
	handoffs := func(n int) []string {
		return []string{strings.Repeat("public\n {\"a\":1}\n", n)}
	}
	used := func(chunks []string) uint64 {
		return allocatedBytes(func() { maskWith(newRedactor(), chunks) })
	}
	small, large := used(handoffs(2000)), used(handoffs(4000))
	assert.Less(t, float64(large), 2.5*float64(small), "small %d large %d", small, large)

	longLine := splitEvery("[progress "+strings.Repeat("x", maxJSONRecordBytes-20)+"\n", 1)
	assert.Less(t, used(longLine), uint64(16<<20))
}

func BenchmarkMaskerHandoffsOneWrite(b *testing.B) {
	input := strings.Repeat("public\n {\"a\":1}\n", 8000)
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		maskWith(newRedactor(), []string{input})
	}
}

func BenchmarkMaskerOrdinaryLines(b *testing.B) {
	input := strings.Repeat("2026/01/02 15:04:05 INFO step done key=value count=3\n", 10<<20/56)
	chunks := splitEvery(input, 4096)
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		maskWith(newRedactor(), chunks)
	}
}

func BenchmarkMaskerLongLineByteAtATime(b *testing.B) {
	for name, prefix := range map[string]string{"plain": "public ", "bracket": "[progress "} {
		chunks := splitEvery(prefix+strings.Repeat("x", maxJSONRecordBytes)+"\n", 1)
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				maskWith(newRedactor(), chunks)
			}
		})
	}
}

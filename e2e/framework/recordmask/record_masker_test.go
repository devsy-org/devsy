package recordmask

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
)

func runMasker(t *testing.T, chunks []string) string {
	t.Helper()
	var out strings.Builder
	redactor := redact.New(redact.Options{})
	masker := New(redactor, func(text string) { out.WriteString(text) })
	for _, chunk := range chunks {
		masker.Write(chunk)
	}
	masker.Finish()
	return out.String()
}

func splitEvery(text string, size int) []string {
	var chunks []string
	for len(text) > size {
		chunks = append(chunks, text[:size])
		text = text[size:]
	}
	return append(chunks, text)
}

func oversizedRecord(tail string) string {
	return "{\n  \"Note\": \"" + strings.Repeat("a", maxJSONRecordBytes+10) + "\",\n" + tail
}

func TestRecordMaskerOversizedRecordKeepsMasking(t *testing.T) {
	record := oversizedRecord("  \"Cookie\": \"server-cookie\"\n}\n")
	cut := strings.Index(record, "server-cookie")
	limit := maxJSONRecordBytes
	tiny := append(splitEvery(record[:cut-20], 30000), splitEvery(record[cut-20:], 3)...)
	splits := map[string][]string{
		"whole":          {record},
		"mid key":        {record[:cut-12], record[cut-12:]},
		"mid value":      {record[:cut+6], record[cut+6:]},
		"limit boundary": {record[:limit], record[limit : limit+1], record[limit+1:]},
		"bytes":          splitEvery(record, 1000),
		"tiny tail":      tiny,
	}
	for name, chunks := range splits {
		t.Run(name, func(t *testing.T) {
			got := runMasker(t, chunks)
			if strings.Contains(got, "server-cookie") {
				t.Fatalf("credential leaked: %q", got)
			}
		})
	}
}

func TestRecordMaskerResumesAfterOversizedRecord(t *testing.T) {
	input := oversizedRecord("  \"Cookie\": \"server-cookie\", \"x\": \"}\\\"{\"\n}\n") +
		"{\"Cookie\": \"next-cookie\"}\npublic detail\n"
	for _, size := range []int{len(input), 4096, 7} {
		got := runMasker(t, splitEvery(input, size))
		if strings.Contains(got, "server-cookie") || strings.Contains(got, "next-cookie") {
			t.Fatalf("size %d: credential leaked", size)
		}
		if !strings.HasSuffix(got, "public detail\n") {
			t.Fatalf("size %d: plain line lost: %q", size, got[max(0, len(got)-80):])
		}
	}
}

func TestRecordMaskerMixedOutput(t *testing.T) {
	input := "public log\n{\n  \"Cookie\": \"server-cookie\"\n}\npublic tail"
	for _, size := range []int{len(input), 1, 3, 11, 20} {
		got := runMasker(t, splitEvery(input, size))
		if strings.Contains(got, "server-cookie") {
			t.Fatalf("size %d: leaked: %q", size, got)
		}
		first := strings.Index(got, "public log")
		last := strings.Index(got, "public tail")
		if first != 0 || last < first {
			t.Fatalf("size %d: order lost: %q", size, got)
		}
	}
}

func TestRecordMaskerCloseWhileOversizedOpen(t *testing.T) {
	record := oversizedRecord("  \"Cookie\": \"server-cookie")
	got := runMasker(t, splitEvery(record, 5000))
	if strings.Contains(got, "server-cookie") {
		t.Fatalf("leaked: %q", got)
	}
}

func TestRecordMaskerOversizedRecordCarriesState(t *testing.T) {
	pad := strings.Repeat("p", maxJSONRecordBytes+10)
	cookie := "\"Cookie\": \"server-cookie\"}\n"
	records := map[string]string{
		"nested object": "{\"a\":{\"pad\":\"" + pad + "\",\"c\":1},\n" + cookie,
		"inside string": "{\"pad\":\"" + pad + " } \\\" ] still in string\",\n" + cookie,
		"after escape":  "{\"pad\":\"" + pad + "\\\\\\\"}\",\n" + cookie,
		"nested array":  "{\"a\":[{\"pad\":\"" + pad + "\"}],\n" + cookie,
	}
	for name, record := range records {
		limit := maxJSONRecordBytes
		pos := strings.Index(record, "p")
		splits := map[string][]string{
			"whole":    {record},
			"at limit": {record[:limit], record[limit:]},
			"after":    {record[:limit+1], record[limit+1:]},
			"one byte": append([]string{record[:limit-1], record[limit-1 : limit]},
				splitEvery(record[limit:], 1)...),
			"in pad": {record[:pos+limit-5], record[pos+limit-5:]},
			"chunks": splitEvery(record, 4096),
		}
		for split, chunks := range splits {
			t.Run(name+"/"+split, func(t *testing.T) {
				chunks = append(chunks, "public detail\n")
				got := runMasker(t, chunks)
				if strings.Contains(got, "server-cookie") {
					t.Fatalf("credential leaked: %q", got[max(0, len(got)-120):])
				}
				if !strings.HasSuffix(got, "public detail\n") {
					t.Fatalf("plain line lost: %q", got[max(0, len(got)-80):])
				}
			})
		}
	}
}

func TestRecordMaskerPlainBracketLineOverLimit(t *testing.T) {
	line := "[progress " + strings.Repeat("x", maxJSONRecordBytes+10)
	inputs := map[string]string{
		"lf":   line + "\npublic detail\n",
		"crlf": line + "\r\npublic detail\r\n",
	}
	for name, input := range inputs {
		cut := len(line) + 1
		splits := map[string][]string{
			"one write": {input},
			"32769":     splitEvery(input, 32769),
			"65537":     splitEvery(input, 65537),
			"split eol": {input[:cut], input[cut:]},
		}
		for split, chunks := range splits {
			t.Run(name+"/"+split, func(t *testing.T) {
				got := runMasker(t, chunks)
				if !strings.Contains(got, "public detail") {
					t.Fatalf("later line lost: %q", got[max(0, len(got)-80):])
				}
			})
		}
	}
}

func TestRecordMaskerOverflowTailSyntaxError(t *testing.T) {
	head := "{\"pad\":\"" + strings.Repeat("a", maxJSONRecordBytes+10) + "\","
	tail := "\n\"Cookie\": \"server-cookie\"}\npublic detail\n"
	splits := map[string][]string{
		"error in overflow write": {head + " !", tail},
		"error in later write":    {head, " !" + tail},
	}
	for name, chunks := range splits {
		t.Run(name, func(t *testing.T) {
			got := runMasker(t, chunks)
			if strings.Contains(got, "server-cookie") {
				t.Fatalf("credential leaked: %q", got[max(0, len(got)-120):])
			}
			if !strings.HasSuffix(got, "public detail\n") {
				t.Fatalf("plain line lost: %q", got[max(0, len(got)-80):])
			}
		})
	}
}

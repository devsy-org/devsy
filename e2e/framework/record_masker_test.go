package framework

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
)

func runMasker(t *testing.T, chunks []string) string {
	t.Helper()
	var out strings.Builder
	redactor := redact.New(redact.Options{})
	masker := newRecordMasker(redactor, func(text string) { out.WriteString(text) })
	for _, chunk := range chunks {
		masker.write(chunk)
	}
	masker.finish()
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

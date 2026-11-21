package secrets_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/suite"
)

type StreamingBoundarySuite struct{ suite.Suite }

func TestStreamingBoundarySuite(t *testing.T) { suite.Run(t, new(StreamingBoundarySuite)) }

func (s *StreamingBoundarySuite) TestKnownSecretsAcrossEverySplit() {
	for _, entries := range [][]string{
		{"TOKEN=abcabc"},
		{"TOKEN=aaaaa"},
		{"TOKEN=abababa"},
		{"A=abcabcX", "B=abcabc"},
		{"A=aba", "B=bab"},
		{"TOKEN=abc\x00\xffabc"},
	} {
		redactor := redact.FromEntries(entries)
		var values []string
		for _, entry := range entries {
			_, value, _ := strings.Cut(entry, "=")
			values = append(values, value)
		}
		input := "ordinary " + strings.Join(values, " ")
		want := redactor.Redact(input)
		for split := 0; split <= len(input); split++ {
			stream := redact.NewStream(redactor)
			got := stream.RedactChunk(
				input[:split],
			) + stream.RedactChunk(
				input[split:],
			) + stream.Flush()
			s.Equal(want, got, "entries %v, split %d", entries, split)
		}
		stream := redact.NewStream(redactor)
		var output strings.Builder
		for index := range len(input) {
			output.WriteString(stream.RedactChunk(input[index : index+1]))
		}
		output.WriteString(stream.Flush())
		s.Equal(want, output.String(), "one-byte chunks, entries %v", entries)
	}
}

func BenchmarkStreamingBoundaryRetainedValues(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			entries := make([]string, count)
			for index := range count {
				entries[index] = fmt.Sprintf("TOKEN=private-%d-value", index)
			}
			redactor := redact.FromEntries(entries)
			input := strings.Repeat("ordinary output ", 2048) + "private-"
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				stream := redact.NewStream(redactor)
				_ = stream.RedactChunk(input)
				_ = stream.Flush()
			}
		})
	}
}

func (s *StreamingBoundarySuite) TestRepeatedOutputDoesNotBufferWholeStream() {
	const count = 1 << 14
	input := strings.Repeat("abcabc", count)
	redactor := redact.FromEntries([]string{"TOKEN=abcabc"})
	stream := redact.NewStream(redactor)
	emitted := stream.RedactChunk(input)
	s.GreaterOrEqual(len(emitted), 3*(count-1))
	s.Equal(redactor.Redact(input), emitted+stream.Flush())
}

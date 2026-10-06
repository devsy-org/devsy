package secrets_test

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
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
	} {
		redactor := secrets.NewRedactor(entries)
		var values []string
		for _, entry := range entries {
			_, value, _ := strings.Cut(entry, "=")
			values = append(values, value)
		}
		input := "ordinary " + strings.Join(values, " ")
		want := redactor.Redact(input)
		for split := 0; split <= len(input); split++ {
			stream := secrets.NewStreamingRedactor(redactor)
			got := stream.RedactChunk(
				input[:split],
			) + stream.RedactChunk(
				input[split:],
			) + stream.Flush()
			s.Equal(want, got, "entries %v, split %d", entries, split)
		}
		stream := secrets.NewStreamingRedactor(redactor)
		var output strings.Builder
		for index := range len(input) {
			output.WriteString(stream.RedactChunk(input[index : index+1]))
		}
		output.WriteString(stream.Flush())
		s.Equal(want, output.String(), "one-byte chunks, entries %v", entries)
	}
}

func (s *StreamingBoundarySuite) TestRepeatedOutputDoesNotBufferWholeStream() {
	const count = 1 << 14
	input := strings.Repeat("abcabc", count)
	redactor := secrets.NewRedactor([]string{"TOKEN=abcabc"})
	stream := secrets.NewStreamingRedactor(redactor)
	emitted := stream.RedactChunk(input)
	s.GreaterOrEqual(len(emitted), 3*(count-1))
	s.Equal(redactor.Redact(input), emitted+stream.Flush())
}

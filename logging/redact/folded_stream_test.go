package redact

import (
	"strings"
	"testing"
)

func TestDiagnosticStreamFoldedAuthorization(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{
			"crlf",
			"Authorization:\r\n Bearer fixture\r\n\r\n public\r\n",
			"Authorization:\r\n***\r\n\r\n public\r\n",
		},
		{
			"scheme",
			"Proxy-Authorization = \nBasic fixture\npublic",
			"Proxy-Authorization = \n***\npublic",
		},
		{
			"several",
			"Authorization: Bearer first\n\tsecond\r third\rpublic",
			"Authorization: ***\n***\r***\rpublic",
		},
		{"blank", "Authorization:\n \t\n public", "Authorization:\n \t\n public"},
		{
			"separate",
			"Authorization:\nBearerish public\n public",
			"Authorization:\nBearerish public\n public",
		},
		{
			"multiple headers",
			"Authorization: first Authorization:\nBearer fixture",
			"Authorization: ***\n***",
		},
		{
			"json string",
			`{"msg":"Authorization:\n Bearer fixture","ok":true}`,
			`{"msg":"Authorization:***","ok":true}`,
		},
		{
			"json boundary",
			"{\"msg\":\"Authorization:\"}\n public",
			"{\"msg\":\"Authorization:\"}\n public",
		},
		{
			"conservative url",
			"ssh://user:part/fixture@host.test/path\npublic",
			"ssh://***@host.test/path\npublic",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for split := 0; split <= len(test.input); split++ {
				stream := NewDiagnosticStream(New(Options{}))
				got := stream.RedactChunk(
					test.input[:split],
				) + stream.RedactChunk(
					test.input[split:],
				) + stream.Flush()
				if got != test.want {
					t.Fatalf("split %d: got %q, want %q", split, got, test.want)
				}
			}
		})
	}
}

func TestDiagnosticStreamOversizedAuthorizationContinuation(t *testing.T) {
	long := strings.Repeat("x", streamLineLimit+1)
	for _, input := range []string{
		"Authorization:\n Bearer " + long + "\npublic",
		"Authorization:\nBearer " + long + "\npublic",
		"Authorization: " + long + "\n folded-fixture\npublic",
		long + " Authorization:\n folded-fixture\npublic",
	} {
		for _, split := range []int{0, streamLineLimit, len(input) - len("public"), len(input)} {
			stream := NewDiagnosticStream(New(Options{}))
			output := stream.RedactChunk(
				input[:split],
			) + stream.RedactChunk(
				input[split:],
			) + stream.Flush()
			if !strings.HasSuffix(output, "\npublic") ||
				strings.Contains(output, "folded-fixture") ||
				(strings.HasPrefix(input, "Authorization:") && strings.Contains(output, long)) {
				t.Fatalf("split %d: credential leaked or public message disappeared", split)
			}
		}
	}
	stream := NewDiagnosticStream(New(Options{}))
	if got := stream.RedactChunk("Authorization:\n" + long); got != "Authorization:\n"+long {
		t.Fatal("an unrelated large plain record lost prompt output")
	}
}

func TestOrdinaryStreamDoesNotJoinAuthorizationRecords(t *testing.T) {
	const input = "Authorization:\n Bearer fixture\n"
	stream := NewStream(New(Options{}))
	if got := stream.RedactChunk(input) + stream.Flush(); got != input {
		t.Fatalf("ordinary stream changed behavior: %q", got)
	}
}

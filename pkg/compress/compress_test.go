package compress

import (
	"testing"
)

func TestCompressEmpty(t *testing.T) {
	got, err := Compress("")
	if err != nil {
		t.Fatalf("Compress(\"\") error: %v", err)
	}
	if got != "" {
		t.Fatalf("Compress(\"\") = %q, want \"\"", got)
	}
}

func TestDecompressEmpty(t *testing.T) {
	got, err := Decompress("")
	if err != nil {
		t.Fatalf("Decompress(\"\") error: %v", err)
	}
	if got != "" {
		t.Fatalf("Decompress(\"\") = %q, want \"\"", got)
	}
}

func TestCompressDecompressRoundTrip(t *testing.T) {
	cases := []string{
		"a",
		"hello world",
		"the quick brown fox jumps over the lazy dog",
		"\x00\x01\x02 binary \xff\xfe data",
	}
	for _, in := range cases {
		compressed, err := Compress(in)
		if err != nil {
			t.Fatalf("Compress(%q) error: %v", in, err)
		}
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress(%q) error: %v", compressed, err)
		}
		if got != in {
			t.Fatalf("round-trip mismatch: got %q, want %q", got, in)
		}
	}
}

func TestCompressOutputIsBase64(t *testing.T) {
	compressed, err := Compress("decodable")
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	for _, r := range compressed {
		isBase64Std := (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '+' || r == '/' || r == '='
		if !isBase64Std {
			t.Fatalf("Compress produced non-base64 char %q in %q", r, compressed)
		}
	}
}

func TestDecompressInvalidBase64(t *testing.T) {
	if _, err := Decompress("!!!not-base64!!!"); err == nil {
		t.Fatal("Decompress: expected error for invalid base64, got nil")
	}
}

func TestDecompressInvalidGzip(t *testing.T) {
	// "aGVsbG8=" decodes to "hello" which is not valid gzip data.
	if _, err := Decompress("aGVsbG8="); err == nil {
		t.Fatal("Decompress: expected error for non-gzip input, got nil")
	}
}

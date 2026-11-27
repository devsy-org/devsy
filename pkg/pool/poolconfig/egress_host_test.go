package poolconfig

import "testing"

const (
	regHost   = "registry.example.net"
	bareLabel = "localhost"
)

func TestNormalizeHost(t *testing.T) {
	good := map[string]string{
		regHost:                 regHost,
		"Registry.Example.NET":  regHost,
		"registry.example.net.": regHost,
		"A.B.Example.com.":      "a.b.example.com",
	}
	for in, want := range good {
		got, ok := NormalizeHost(in)
		if !ok || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"", ".", "registry.example.net..", "a..b.com", ".registry.example.net", bareLabel, "9.9.9.9", "::1",
		"127.1", "x.internal", "metadata.goog", "g\u0130thub.com", "registry.example.net\x00", "a b.com",
		"registry.example.net:443", "*.registry.example.net", "-a.com", "a-.com",
	} {
		if got, ok := NormalizeHost(in); ok {
			t.Errorf("NormalizeHost(%q) = %q, want refused", in, got)
		}
	}
}

func TestValidHostPattern(t *testing.T) {
	for _, in := range []string{regHost, "*.registry.example.net", "a.b.example.org"} {
		if !ValidHostPattern(in) {
			t.Errorf("ValidHostPattern(%q) = false", in)
		}
	}
	for _, in := range []string{
		"", "*", "*.", "a.*.com", "**.registry.example.net", "GitHub.com", "registry.example.net.", "9.9.9.9",
		bareLabel, "*.internal", "github",
	} {
		if ValidHostPattern(in) {
			t.Errorf("ValidHostPattern(%q) = true", in)
		}
	}
}

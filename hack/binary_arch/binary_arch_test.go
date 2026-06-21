package binaryarch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildFixture(t *testing.T, goos, goarch string) string {
	t.Helper()

	src := filepath.Join(t.TempDir(), "prog.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write fixture src: %v", err)
	}

	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-build %s/%s: %v\n%s", goos, goarch, err, combined)
	}
	return out
}

func TestFromFile(t *testing.T) {
	cases := []Arch{
		{GOOS: OSDarwin, GOARCH: ArchAMD64},
		{GOOS: OSDarwin, GOARCH: ArchARM64},
		{GOOS: OSLinux, GOARCH: ArchAMD64},
		{GOOS: OSLinux, GOARCH: ArchARM64},
		{GOOS: OSWindows, GOARCH: ArchAMD64},
	}
	for _, want := range cases {
		t.Run(want.String(), func(t *testing.T) {
			got, err := FromFile(buildFixture(t, want.GOOS, want.GOARCH))
			if err != nil {
				t.Fatalf("FromFile: %v", err)
			}
			if got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

func TestFromFileRejectsUnknownHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "garbage")
	if err := os.WriteFile(p, []byte("not a binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FromFile(p); err == nil {
		t.Fatal("expected error for non-binary file")
	}
}

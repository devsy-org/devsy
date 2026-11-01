package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	reportBody = "# Licenses\n"
	inSyncMsg  = "THIRD_PARTY_LICENSES.md is up to date."
)

func TestNormalizeTrailingNewline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "no trailing newline", in: "# Licenses", want: reportBody},
		{name: "one trailing newline", in: reportBody, want: reportBody},
		{name: "many trailing newlines", in: "# Licenses\n\n\n", want: reportBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out.md")
			fatalOnErr(t, os.WriteFile(path, []byte(tt.in), 0o644))

			fatalOnErr(t, normalizeTrailingNewline(path))

			got, err := os.ReadFile(path)
			fatalOnErr(t, err)
			if string(got) != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRunDetectorFeedsDepsAndNormalizesOutput checks the detector receives the
// deps stream on stdin and that its output lands at the requested path with
// one trailing newline.
func TestRunDetectorFeedsDepsAndNormalizesOutput(t *testing.T) {
	fake := &fakeProcesses{detectorOut: reportBody + "\n"}
	g := generator{root: t.TempDir(), runCmd: fake.run, stderr: io.Discard}
	deps := []byte("{\n\t\"Path\": \"a.example/a\"\n}\n")
	outPath := filepath.Join(t.TempDir(), "THIRD_PARTY_LICENSES.md")

	fatalOnErr(t, g.runDetector(filepath.Join(g.root, "hack", "licenses"), deps, outPath))

	if !bytes.Equal(fake.detectorIn, deps) {
		t.Errorf("detector stdin = %q, want %q", fake.detectorIn, deps)
	}
	got, err := os.ReadFile(outPath)
	fatalOnErr(t, err)
	if string(got) != reportBody {
		t.Errorf("output = %q, want %q", got, reportBody)
	}
}

// TestCheckInSync pins current check-mode behaviour: an out-of-date file
// prints a warning and still returns nil (see PR Notes). A missing file is
// an error.
func TestCheckInSync(t *testing.T) {
	tests := []struct {
		name       string
		current    *string
		detector   string
		wantErr    bool
		wantStderr string
	}{
		{
			name:       "in sync",
			current:    new(reportBody),
			detector:   reportBody,
			wantStderr: inSyncMsg,
		},
		{
			name:       "trailing newline drift is in sync",
			current:    new(reportBody),
			detector:   "# Licenses\n\n\n",
			wantStderr: inSyncMsg,
		},
		{
			name:       "out of date warns and returns nil",
			current:    new("# Old\n"),
			detector:   "# New\n",
			wantStderr: "warning: THIRD_PARTY_LICENSES.md is out of date",
		},
		{
			name:     "missing file is an error",
			current:  nil,
			detector: reportBody,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			outFile := filepath.Join(dir, "THIRD_PARTY_LICENSES.md")
			if tt.current != nil {
				fatalOnErr(t, os.WriteFile(outFile, []byte(*tt.current), 0o644))
			}
			fake := &fakeProcesses{detectorOut: tt.detector}
			var stderr bytes.Buffer
			g := generator{root: dir, runCmd: fake.run, stderr: &stderr}

			err := g.checkInSync(filepath.Join(dir, "hack", "licenses"), outFile, []byte("{}\n"))

			if tt.wantErr {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("err = %v, want missing-file error", err)
				}
				return
			}
			fatalOnErr(t, err)
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

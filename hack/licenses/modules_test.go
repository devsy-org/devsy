package main

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
)

// pkgStream encodes packages as `go list -deps -json` emits them.
func pkgStream(t *testing.T, pkgs ...goPackage) string {
	t.Helper()
	var b strings.Builder
	for _, p := range pkgs {
		data, err := json.Marshal(p)
		fatalOnErr(t, err)
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}

func mod(path, version string) *goModule {
	return &goModule{Path: path, Version: version, Dir: "/gomod/" + path + "@" + version}
}

func TestDecodeModules(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		want    []string
		wantErr bool
	}{
		{
			name: "keeps third-party modules only",
			stream: pkgStream(t,
				goPackage{Module: mod("a.example/a", "v1.0.0")},
				goPackage{},
				goPackage{Module: &goModule{Path: "example.com/main", Main: true}},
				goPackage{Module: &goModule{Path: ""}},
			),
			want: []string{"a.example/a"},
		},
		{
			name:   "empty stream yields no modules",
			stream: "",
			want:   nil,
		},
		{
			name:    "malformed json is an error",
			stream:  "{",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeModules(strings.NewReader(tt.stream))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if gotPaths := slices.Sorted(maps.Keys(got)); !slices.Equal(gotPaths, tt.want) {
				t.Errorf("paths = %v, want %v", gotPaths, tt.want)
			}
		})
	}
}

// TestModuleListJSONMergesTargetsDeterministically checks that per-target
// lists are unioned, main/empty entries never leak through, targets with no
// output contribute nothing, and output is sorted by path.
func TestModuleListJSONMergesTargetsDeterministically(t *testing.T) {
	fake := &fakeProcesses{
		listOut: map[string]string{
			"linux/amd64": pkgStream(t,
				goPackage{Module: mod("c.example/c", "v3.0.0")},
				goPackage{Module: mod("a.example/a", "v1.0.0")},
			),
			"linux/arm64": pkgStream(t,
				goPackage{Module: mod("b.example/b", "v2.0.0")},
				goPackage{Module: mod("a.example/a", "v1.0.0")},
				goPackage{Module: &goModule{Path: "example.com/main", Main: true}},
			),
			"windows/amd64": pkgStream(t, goPackage{Module: mod("b.example/b", "v2.0.0")}),
		},
	}
	g := generator{root: t.TempDir(), runCmd: fake.run, stderr: io.Discard}

	got, err := g.moduleListJSON()
	fatalOnErr(t, err)

	want := lines(
		"{",
		"\t\"Path\": \"a.example/a\",",
		"\t\"Version\": \"v1.0.0\",",
		"\t\"Main\": false,",
		"\t\"Dir\": \"/gomod/a.example/a@v1.0.0\"",
		"}",
		"{",
		"\t\"Path\": \"b.example/b\",",
		"\t\"Version\": \"v2.0.0\",",
		"\t\"Main\": false,",
		"\t\"Dir\": \"/gomod/b.example/b@v2.0.0\"",
		"}",
		"{",
		"\t\"Path\": \"c.example/c\",",
		"\t\"Version\": \"v3.0.0\",",
		"\t\"Main\": false,",
		"\t\"Dir\": \"/gomod/c.example/c@v3.0.0\"",
		"}",
	)
	if string(got) != want {
		t.Errorf("moduleListJSON =\n%s\nwant\n%s", got, want)
	}
}

func TestModuleListJSONReportsFailingTarget(t *testing.T) {
	fake := &fakeProcesses{
		listErr: map[string]error{"darwin/arm64": io.ErrUnexpectedEOF},
	}
	g := generator{root: t.TempDir(), runCmd: fake.run, stderr: io.Discard}

	_, err := g.moduleListJSON()
	if err == nil || !strings.Contains(err.Error(), "listing deps for darwin/arm64") {
		t.Fatalf("err = %v, want wrapped darwin/arm64 failure", err)
	}
}

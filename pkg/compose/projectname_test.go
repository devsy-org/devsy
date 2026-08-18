package compose

import (
	"os"
	"path/filepath"
	"testing"
)

const noNameBody = "services:\n  web: { image: nginx }\n"

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestProjectNameFromEnvFiles(t *testing.T) {
	dir := t.TempDir()
	first := writeTestFile(t, dir, "a.env", "COMPOSE_PROJECT_NAME=first\nOTHER=1\n")
	second := writeTestFile(t, dir, "b.env", "COMPOSE_PROJECT_NAME=second\n")
	none := writeTestFile(t, dir, "none.env", "OTHER=1\n")
	set := writeTestFile(t, dir, "set.env", "COMPOSE_PROJECT_NAME=found\n")
	emptyA := writeTestFile(t, dir, "empty-a.env", "OTHER=1\n")
	emptyB := writeTestFile(t, dir, "empty-b.env", "FOO=bar\n")

	cases := []struct {
		name    string
		files   []string
		want    string
		wantErr bool
	}{
		{name: "first non-empty value in order", files: []string{first, second}, want: "first"},
		{name: "skips files without the variable", files: []string{none, set}, want: "found"},
		{name: "empty when none define it", files: []string{emptyA, emptyB}, want: ""},
		{
			name:  "skips missing files without error",
			files: []string{filepath.Join(dir, "nope.env")},
			want:  "",
		},
		{name: "empty input returns empty", files: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectNameFromEnvFiles(tc.files)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComposeFilesDeclareName(t *testing.T) {
	dir := t.TempDir()
	single := writeTestFile(t, dir, "single.yml", "name: myapp\nservices: {}\n")
	noname := writeTestFile(t, dir, "noname.yml", noNameBody)
	named := writeTestFile(t, dir, "named.yml", "name: named-app\nservices: {}\n")
	noNameA := writeTestFile(t, dir, "no-name-a.yml", noNameBody)
	noNameB := writeTestFile(t, dir, "no-name-b.yml", noNameBody)
	bad := writeTestFile(t, dir, "bad.yml", "name: [unterminated\n")
	good := writeTestFile(t, dir, "good.yml", "name: good-app\nservices: {}\n")
	interpolated := writeTestFile(
		t, dir, "interpolated.yml", "name: ${COMPOSE_PROJECT_NAME:-myapp}\nservices: {}\n",
	)

	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		{name: "single file with name", files: []string{single}, want: true},
		{
			name:  "skips files without name and finds one that has it",
			files: []string{noname, named},
			want:  true,
		},
		{name: "false when no file declares name", files: []string{noNameA, noNameB}, want: false},
		{
			name:  "skips missing files without error",
			files: []string{filepath.Join(dir, "missing.yml")},
			want:  false,
		},
		{name: "ignores unparseable YAML gracefully", files: []string{bad, good}, want: true},
		{
			name:  "raw declaration counts even when unresolved (compose-go interpolates the value)",
			files: []string{interpolated},
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComposeFilesDeclareName(tc.files)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSanitizeProjectName(t *testing.T) {
	h := &ComposeHelper{Version: "2.30.0"}
	cases := []struct{ in, want string }{
		{"MyApp", "myapp"},
		{"foo bar baz", "foobarbaz"},
		{"UPPER-Case_99", "upper-case_99"},
		{"café", "caf"},
	}
	for _, c := range cases {
		if got := h.SanitizeProjectName(c.in); got != c.want {
			t.Errorf("SanitizeProjectName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

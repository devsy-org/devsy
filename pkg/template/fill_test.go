package template

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/assert"
)

func TestFillTemplate(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		vars     map[string]string
		want     string
		wantErr  bool
		errMatch string
	}{
		{
			name: "simple substitution",
			tmpl: "hello {{.Name}}",
			vars: map[string]string{"Name": "devsy"},
			want: "hello devsy",
		},
		{
			name: "multiple vars",
			tmpl: "{{.First}}-{{.Second}}",
			vars: map[string]string{"First": "a", "Second": "b"},
			want: "a-b",
		},
		{
			name: "repeated var reused",
			tmpl: "{{.Tag}} and {{.Tag}}",
			vars: map[string]string{"Tag": "x"},
			want: "x and x",
		},
		{
			name: "no vars passes through unchanged",
			tmpl: "plain text with no placeholders",
			vars: map[string]string{},
			want: "plain text with no placeholders",
		},
		{
			name: "missing var renders no-value sentinel",
			tmpl: "[{{.Missing}}]",
			vars: map[string]string{"Other": "y"},
			want: "[<no value>]",
		},
		{
			name:    "unclosed action is a parse error",
			tmpl:    "hello {{.Name",
			vars:    map[string]string{"Name": "devsy"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FillTemplate(tc.tmpl, tc.vars)
			if tc.wantErr {
				assert.ErrorContains(t, err, tc.errMatch)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFillTemplateEmpty(t *testing.T) {
	got, err := FillTemplate("", map[string]string{})
	assert.NilError(t, err)
	assert.Equal(t, "", got)
}

func TestWriteFiles(t *testing.T) {
	folder := t.TempDir()
	files := map[string]string{
		"a.txt": "alpha",
		"b.txt": "beta content",
	}

	err := WriteFiles(folder, files)
	assert.NilError(t, err)
	for rel, want := range files {
		path := filepath.Join(folder, rel)
		got, err := os.ReadFile(path) // #nosec G304 -- test-controlled temp path
		assert.NilError(t, err)
		assert.Equal(t, want, string(got))

		info, err := os.Stat(path)
		assert.NilError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestWriteFilesNestedPathFailsWithoutMkdir(t *testing.T) {
	folder := t.TempDir()
	err := WriteFiles(folder, map[string]string{"sub/a.txt": "nested content"})
	assert.ErrorContains(t, err, "no such file or directory")
}

func TestWriteFilesErrorOnMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	err := WriteFiles(missing, map[string]string{"a.txt": "orphan content"})
	assert.ErrorContains(t, err, "no such file or directory")
}

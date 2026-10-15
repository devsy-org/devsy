package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// filterFixture returns a repo root that does not exist on disk and a helper
// that joins slash-separated relative paths onto it.
func filterFixture(t *testing.T) (root string, at func(string) string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "devsy")
	at = func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	return root, at
}

func TestFilterUnitTestPackagesKeepsPackages(t *testing.T) {
	t.Parallel()

	root, at := filterFixture(t)

	tests := []struct {
		name   string
		goList string
		want   []string
	}{
		{
			name:   "keeps package under root",
			goList: "example.com/devsy/pkg/foo|" + at("pkg/foo") + "\n",
			want:   []string{"example.com/devsy/pkg/foo"},
		},
		{
			name:   "keeps root package",
			goList: "example.com/devsy|" + root + "\n",
			want:   []string{"example.com/devsy"},
		},
		{
			name: "excludes test and e2e directories",
			goList: strings.Join([]string{
				"example.com/devsy/test/helpers|" + at("test/helpers"),
				"example.com/devsy/pkg/test|" + at("pkg/test"),
				"example.com/devsy/e2e|" + at("e2e"),
				"example.com/devsy/e2e/framework|" + at("e2e/framework"),
				"example.com/devsy/pkg/e2e/x|" + at("pkg/e2e/x"),
				"example.com/devsy/pkg/foo|" + at("pkg/foo"),
			}, "\n") + "\n",
			want: []string{"example.com/devsy/pkg/foo"},
		},
		{
			name: "keeps names that only contain test or e2e",
			goList: strings.Join([]string{
				"example.com/devsy/pkg/testing|" + at("pkg/testing"),
				"example.com/devsy/pkg/tests|" + at("pkg/tests"),
				"example.com/devsy/pkg/e2equal|" + at("pkg/e2equal"),
			}, "\n") + "\n",
			want: []string{
				"example.com/devsy/pkg/testing",
				"example.com/devsy/pkg/tests",
				"example.com/devsy/pkg/e2equal",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := filterUnitTestPackages(tc.goList, root)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("packages = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterUnitTestPackagesRejects(t *testing.T) {
	t.Parallel()

	root, at := filterFixture(t)
	sibling := root + "2"

	tests := []struct {
		name    string
		goList  string
		wantErr error
	}{
		{
			name:    "rejects sibling sharing root prefix",
			goList:  "example.com/x|" + filepath.Join(sibling, "x") + "\n",
			wantErr: errOutsideRepository,
		},
		{
			name: "rejects directory outside root",
			goList: "example.com/elsewhere|" + filepath.Join(
				filepath.Dir(root),
				"elsewhere",
			) + "\n",
			wantErr: errOutsideRepository,
		},
		{
			name:    "rejects listing where every package is excluded",
			goList:  "example.com/devsy/e2e|" + at("e2e") + "\n",
			wantErr: errNoUnitTestPackages,
		},
		{
			name:    "rejects empty listing",
			goList:  "",
			wantErr: errNoUnitTestPackages,
		},
		{
			name:    "rejects line without separator",
			goList:  "example.com/devsy/pkg/foo\n",
			wantErr: errMalformedGoList,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := filterUnitTestPackages(tc.goList, root)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

package provider

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestErrVersionListUnsupported(t *testing.T) {
	if ErrVersionListUnsupported == nil {
		t.Fatal("ErrVersionListUnsupported must be defined")
	}
	wrapped := errors.New("wrapped: " + ErrVersionListUnsupported.Error())
	if !errors.Is(errors.Join(ErrVersionListUnsupported, wrapped), ErrVersionListUnsupported) {
		t.Fatal("errors.Is must work against ErrVersionListUnsupported")
	}
}

func TestProviderVersionFields(t *testing.T) {
	v := ProviderVersion{Tag: testTagV100, Current: true}
	if v.Tag != testTagV100 || !v.Current {
		t.Fatal("fields must round-trip")
	}
}

func TestClassifyVersionSource(t *testing.T) {
	cases := []struct {
		in   string
		kind sourceKind
	}{
		{"github.com/devsy-org/devsy-provider-aws@v1.2.0", sourceGitHub},
		{"github.com/devsy-org/devsy-provider-aws", sourceGitHub},
		{"https://example.com/foo/provider.yaml", sourceManifestURL},
		{"https://example.com/foo/provider.yaml@v1.0.0", sourceManifestURL},
		{"/abs/path/provider.yaml", sourceLocal},
		{"./relative/provider.yaml", sourceLocal},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := ClassifyVersionSource(c.in)
			if got != c.kind {
				t.Fatalf("got %v, want %v", got, c.kind)
			}
		})
	}
}

func TestListVersionsForSource_LocalUnsupported(t *testing.T) {
	_, err := ListVersionsForSource("/abs/path/provider.yaml", ListVersionsOptions{})
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("local source must be unsupported, got %v", err)
	}
}

func TestListVersionsForSource_UnknownUnsupported(t *testing.T) {
	_, err := ListVersionsForSource("totally-bogus-source", ListVersionsOptions{})
	if !errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("unknown source must be unsupported, got %v", err)
	}
}

func TestListVersionsForSource_GitHubInvalid(t *testing.T) {
	_, err := ListVersionsForSource("github.com/missingrepo", ListVersionsOptions{})
	if err == nil || errors.Is(err, ErrVersionListUnsupported) {
		t.Fatalf("github source missing repo segment must error (not unsupported): %v", err)
	}
}

func TestMarkCurrent(t *testing.T) {
	versions := []ProviderVersion{{Tag: testTagV100}, {Tag: "v0.9.0"}}
	got := MarkCurrent(versions, "github.com/foo/bar@v1.0.0")
	if !got[0].Current || got[1].Current {
		t.Fatalf("only v1.0.0 should be marked current: %+v", got)
	}
}

func TestMarkCurrent_NoTag(t *testing.T) {
	versions := []ProviderVersion{{Tag: testTagV100}}
	got := MarkCurrent(versions, "github.com/foo/bar")
	if got[0].Current {
		t.Fatal("no pinned tag → none current")
	}
}

func TestRewriteSourceTag(t *testing.T) {
	got, err := RewriteSourceTag("github.com/foo/bar@v1.0.0", "v2.0.0")
	if err != nil || got != "github.com/foo/bar@v2.0.0" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = RewriteSourceTag("github.com/foo/bar", "v2.0.0")
	if err != nil || got != "github.com/foo/bar@v2.0.0" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := RewriteSourceTag("github.com/foo/bar", ""); err == nil {
		t.Fatal("empty tag must error")
	}
}

func TestProviderVersionCheckResult_UnsupportedShape(t *testing.T) {
	// Verify the struct shape and JSON tags by marshalling.
	r := ProviderVersionCheckResult{
		Current:     testTagV100,
		Unsupported: true,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"current":"v1.0.0","latest":"","updateAvailable":false,"unsupported":true}`
	if string(data) != expected {
		t.Fatalf("JSON shape wrong:\ngot:  %s\nwant: %s", data, expected)
	}
}

func TestProviderVersionCheckResult_ErrorShape(t *testing.T) {
	r := ProviderVersionCheckResult{Error: "boom"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"current":"","latest":"","updateAvailable":false,"unsupported":false,"error":"boom"}`
	if string(data) != expected {
		t.Fatalf("JSON shape wrong:\ngot:  %s\nwant: %s", data, expected)
	}
}

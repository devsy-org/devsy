package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"gotest.tools/assert"
)

const (
	testImage  = "python"
	testID     = "default"
	testPath   = ".devcontainer/custom.json"
	testImgSrc = "image:python"
)

func TestParseSourceSpec(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  SourceKind
		wantImage string
		wantID    string
		wantPath  string
		wantNil   bool
		wantErr   bool
	}{
		{in: "", wantNil: true},
		{in: "  ", wantNil: true},
		{in: string(SourceNone), wantKind: SourceNone},
		{in: sourceImagePrefix + testImage, wantKind: SourceImage, wantImage: testImage},
		{
			in:        "image:mcr.microsoft.com/devcontainers/python:3",
			wantKind:  SourceImage,
			wantImage: "mcr.microsoft.com/devcontainers/python:3",
		},
		{in: "image: python ", wantKind: SourceImage, wantImage: "python"},
		{in: sourceIDPrefix + testID, wantKind: SourceID, wantID: testID},
		{in: "id: claude ", wantKind: SourceID, wantID: "claude"},
		{
			in:       testPath,
			wantKind: SourcePath,
			wantPath: testPath,
		},
		{in: "/abs/path.json", wantKind: SourcePath, wantPath: "/abs/path.json"},
		{in: "image:", wantErr: true},
		{in: "id:", wantErr: true},
	}
	for _, c := range cases {
		spec, err := ParseSourceSpec(c.in)
		if c.wantErr {
			assert.Assert(t, err != nil, "input %q should error", c.in)
			continue
		}
		assert.NilError(t, err, "input %q", c.in)
		if c.wantNil {
			assert.Assert(t, spec == nil, "input %q should yield nil spec", c.in)
			continue
		}
		assert.Equal(t, c.wantKind, spec.Kind, "input %q kind", c.in)
		assert.Equal(t, c.wantImage, spec.Image, "input %q image", c.in)
		assert.Equal(t, c.wantID, spec.ID, "input %q id", c.in)
		assert.Equal(t, c.wantPath, spec.Path, "input %q path", c.in)
	}
}

func TestResolveSourceSpec(t *testing.T) {
	cases := []struct {
		in        string
		wantID    string
		wantPath  string
		wantImage string
		wantSrc   string
		wantErr   bool
	}{
		{in: ""},
		{in: sourceIDPrefix + testID, wantID: testID},
		{in: testPath, wantPath: testPath},
		// An external (absolute) path stays in the source for the runner to import.
		{in: "/abs/external/devcontainer.json", wantSrc: "/abs/external/devcontainer.json"},
		// none/image stay in DevContainerSource for agent-side handling.
		{in: string(SourceNone), wantSrc: string(SourceNone)},
		{in: testImgSrc, wantSrc: testImgSrc},
		{in: "image:", wantErr: true},
	}
	for _, c := range cases {
		opts := &provider.CLIOptions{DevContainerSource: c.in}
		err := ResolveSourceSpec(opts)
		if c.wantErr {
			assert.Assert(t, err != nil, "input %q should error", c.in)
			continue
		}
		assert.NilError(t, err, "input %q", c.in)
		assert.Equal(t, c.wantID, opts.DevContainerID, "input %q id", c.in)
		assert.Equal(t, c.wantPath, opts.DevContainerPath, "input %q path", c.in)
		assert.Equal(t, c.wantImage, opts.DevContainerImage, "input %q image", c.in)
		assert.Equal(t, c.wantSrc, opts.DevContainerSource, "input %q source", c.in)
	}
}

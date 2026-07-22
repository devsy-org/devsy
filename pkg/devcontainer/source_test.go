package devcontainer

import (
	"testing"

	"gotest.tools/assert"
)

const testImage = "python"

func TestParseSourceSpec(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  SourceKind
		wantImage string
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
		{in: "image:", wantErr: true},
		{in: "id:default", wantErr: true},
		{in: "bogus", wantErr: true},
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
	}
}

package devcontainer

import (
	"fmt"
	"strings"
)

type SourceSpec struct {
	Kind  SourceKind
	Image string
}

type SourceKind string

const (
	SourceNone  SourceKind = "none"
	SourceImage SourceKind = "image"
)

const sourceImagePrefix = "image:"

func ParseSourceSpec(value string) (*SourceSpec, error) {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return nil, nil
	case value == string(SourceNone):
		return &SourceSpec{Kind: SourceNone}, nil
	case strings.HasPrefix(value, sourceImagePrefix):
		image := strings.TrimSpace(strings.TrimPrefix(value, sourceImagePrefix))
		if image == "" {
			return nil, fmt.Errorf(
				"--devcontainer %q: missing image reference after %q",
				value,
				sourceImagePrefix,
			)
		}
		return &SourceSpec{Kind: SourceImage, Image: image}, nil
	default:
		return nil, fmt.Errorf(
			"--devcontainer %q: unsupported value, expected \"none\" or \"image:<ref>\"",
			value,
		)
	}
}

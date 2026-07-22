package devcontainer

import (
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/flags/names"
)

type SourceSpec struct {
	Kind  SourceKind
	Image string
	ID    string
	Path  string
}

type SourceKind string

const (
	SourceNone  SourceKind = "none"
	SourceImage SourceKind = "image"
	SourceID    SourceKind = "id"
	SourcePath  SourceKind = "path"
)

const (
	sourceImagePrefix = "image:"
	sourceIDPrefix    = "id:"
)

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
				"%s %q: missing image reference after %q",
				names.Flag(names.DevContainer),
				value,
				sourceImagePrefix,
			)
		}
		return &SourceSpec{Kind: SourceImage, Image: image}, nil
	case strings.HasPrefix(value, sourceIDPrefix):
		id := strings.TrimSpace(strings.TrimPrefix(value, sourceIDPrefix))
		if id == "" {
			return nil, fmt.Errorf(
				"%s %q: missing devcontainer id after %q",
				names.Flag(names.DevContainer),
				value,
				sourceIDPrefix,
			)
		}
		return &SourceSpec{Kind: SourceID, ID: id}, nil
	default:
		return &SourceSpec{Kind: SourcePath, Path: value}, nil
	}
}

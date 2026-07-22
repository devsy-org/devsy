package devcontainer

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/provider"
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

func ResolveSourceSpec(opts *provider.CLIOptions) error {
	spec, err := ParseSourceSpec(opts.DevContainerSource)
	if err != nil {
		return err
	}
	if spec == nil {
		return nil
	}
	switch spec.Kind {
	case SourceID:
		opts.DevContainerID = spec.ID
		opts.DevContainerSource = ""
	case SourcePath:
		// An in-repo relative path is a config path; an external absolute
		// path is left in the source so the runner imports it.
		if !filepath.IsAbs(spec.Path) {
			opts.DevContainerPath = spec.Path
			opts.DevContainerSource = ""
		}
	case SourceNone, SourceImage:
	}
	return nil
}

package flags

import (
	"github.com/devsy-org/devsy/pkg/flags/names"
	flag "github.com/spf13/pflag"
)

// DevContainerModifierFlags holds the destinations a command binds the shared
// devcontainer modifier flags to. A nil pointer skips that flag, letting each
// command opt into only the modifiers it supports.
type DevContainerModifierFlags struct {
	Image               *string
	Features            *string
	UserEnvProbe        *string
	IDLabels            *[]string
	ContainerDataFolder *string
}

// RegisterDevContainerModifierFlags registers the devcontainer modifier flags
// shared across the up/build/exec commands so their names and descriptions
// stay in sync.
func RegisterDevContainerModifierFlags(fs *flag.FlagSet, opts DevContainerModifierFlags) {
	if opts.Image != nil {
		fs.StringVar(opts.Image, names.DevContainerImage, "",
			"Override the image in the resolved devcontainer config")
	}
	if opts.Features != nil {
		fs.StringVar(opts.Features, names.Features, "",
			`Extra features to add (JSON, as in the "features" section of devcontainer.json)`)
	}
	if opts.UserEnvProbe != nil {
		fs.StringVar(opts.UserEnvProbe, names.UserEnvProbe, "",
			"Override userEnvProbe (loginInteractiveShell, loginShell, interactiveShell, none)")
	}
	if opts.IDLabels != nil {
		fs.StringArrayVar(opts.IDLabels, names.IDLabel, nil,
			"Container identity label (key=value, repeatable)")
	}
	if opts.ContainerDataFolder != nil {
		fs.StringVar(opts.ContainerDataFolder, names.ContainerDataFolder, "",
			"Path for container-specific data")
	}
}

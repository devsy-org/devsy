package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	RustRoverProductCode           = "RR"
	RustRoverDownloadAmd64Template = "https://download.jetbrains.com/rust/rustrover-%s.tar.gz"
	RustRoverDownloadArm64Template = "https://download.jetbrains.com/rust/rustrover-%s-aarch64.tar.gz"
)

var RustRoverOptions = ide.Options{
	VersionOption: {
		Name:        VersionOption,
		Description: versionOptionDescription,
		Default:     versionOptionDefault,
	},
	DownloadArm64Option: {
		Name:        DownloadArm64Option,
		Description: downloadArm64OptionDescription,
	},
	DownloadAmd64Option: {
		Name:        DownloadAmd64Option,
		Description: downloadAmd64OptionDescription,
	},
}

func NewRustRoverServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		RustRoverOptions,
		values,
		RustRoverProductCode,
		RustRoverDownloadAmd64Template,
		RustRoverDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "rustrover",
		DisplayName:   "RustRover",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

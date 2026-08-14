package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	PhpStormProductCode           = "PS"
	PhpStormDownloadAmd64Template = "https://download.jetbrains.com/webide/PhpStorm-%s.tar.gz"
	PhpStormDownloadArm64Template = "https://download.jetbrains.com/webide/PhpStorm-%s-aarch64.tar.gz"
)

var PhpStormOptions = ide.Options{
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

func NewPhpStorm(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		PhpStormOptions,
		values,
		PhpStormProductCode,
		PhpStormDownloadAmd64Template,
		PhpStormDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "phpstorm",
		DisplayName:   "PhpStorm",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

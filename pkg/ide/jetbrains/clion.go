package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	CLionProductCode           = "CL"
	CLionDownloadAmd64Template = "https://download.jetbrains.com/cpp/CLion-%s.tar.gz"
	CLionDownloadArm64Template = "https://download.jetbrains.com/cpp/CLion-%s-aarch64.tar.gz"
)

var CLionOptions = ide.Options{
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

func NewCLionServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		CLionOptions,
		values,
		CLionProductCode,
		CLionDownloadAmd64Template,
		CLionDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "clion",
		DisplayName:   "CLion",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

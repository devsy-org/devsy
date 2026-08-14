package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	RiderProductCode           = "RD"
	RiderDownloadAmd64Template = "https://download.jetbrains.com/rider/JetBrains.Rider-%s.tar.gz"
	RiderDownloadArm64Template = "https://download.jetbrains.com/rider/JetBrains.Rider-%s-aarch64.tar.gz"
)

var RiderOptions = ide.Options{
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

func NewRiderServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		RiderOptions,
		values,
		RiderProductCode,
		RiderDownloadAmd64Template,
		RiderDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "rider",
		DisplayName:   "Rider",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

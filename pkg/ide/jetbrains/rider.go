package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	RiderProductCode           = "RD"
	RiderDownloadAmd64Template = "https://download.jetbrains.com/rider/JetBrains.Rider-%s.tar.gz"
	RiderDownloadArm64Template = "https://download.jetbrains.com/rider/JetBrains.Rider-%s-aarch64.tar.gz"
)

var RiderOptions = newProductOptions()

func NewRiderServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		RiderOptions,
		values,
		RiderProductCode,
		downloadTemplates{amd64: RiderDownloadAmd64Template, arm64: RiderDownloadArm64Template},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "rider",
		DisplayName:   "Rider",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

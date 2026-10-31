package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	CLionProductCode           = "CL"
	CLionDownloadAmd64Template = "https://download.jetbrains.com/cpp/CLion-%s.tar.gz"
	CLionDownloadArm64Template = "https://download.jetbrains.com/cpp/CLion-%s-aarch64.tar.gz"
)

var CLionOptions = newProductOptions()

func NewCLionServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		CLionOptions,
		values,
		CLionProductCode,
		downloadTemplates{amd64: CLionDownloadAmd64Template, arm64: CLionDownloadArm64Template},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "clion",
		DisplayName:   "CLion",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

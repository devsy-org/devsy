package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	PhpStormProductCode           = "PS"
	PhpStormDownloadAmd64Template = "https://download.jetbrains.com/webide/PhpStorm-%s.tar.gz"
	PhpStormDownloadArm64Template = "https://download.jetbrains.com/webide/PhpStorm-%s-aarch64.tar.gz"
)

var PhpStormOptions = newProductOptions()

func NewPhpStorm(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		PhpStormOptions,
		values,
		PhpStormProductCode,
		downloadTemplates{
			amd64: PhpStormDownloadAmd64Template,
			arm64: PhpStormDownloadArm64Template,
		},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "phpstorm",
		DisplayName:   "PhpStorm",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

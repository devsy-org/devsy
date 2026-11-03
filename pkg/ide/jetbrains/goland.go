package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	GolandProductCode           = "GO"
	GolandDownloadAmd64Template = "https://download.jetbrains.com/go/goland-%s.tar.gz"
	GolandDownloadArm64Template = "https://download.jetbrains.com/go/goland-%s-aarch64.tar.gz"
)

var GolandOptions = newProductOptions()

func NewGolandServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		GolandOptions,
		values,
		GolandProductCode,
		downloadTemplates{amd64: GolandDownloadAmd64Template, arm64: GolandDownloadArm64Template},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "goland",
		DisplayName:   "Goland",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

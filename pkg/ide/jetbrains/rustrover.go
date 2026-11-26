package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	RustRoverProductCode           = "RR"
	RustRoverDownloadAmd64Template = "https://download.jetbrains.com/rust/rustrover-%s.tar.gz"
	RustRoverDownloadArm64Template = "https://download.jetbrains.com/rust/rustrover-%s-aarch64.tar.gz"
)

var RustRoverOptions = newProductOptions()

func NewRustRoverServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		RustRoverOptions,
		values,
		RustRoverProductCode,
		downloadTemplates{
			amd64: RustRoverDownloadAmd64Template,
			arm64: RustRoverDownloadArm64Template,
		},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "rustrover",
		DisplayName:   "RustRover",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

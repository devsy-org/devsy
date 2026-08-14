package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	GolandProductCode           = "GO"
	GolandDownloadAmd64Template = "https://download.jetbrains.com/go/goland-%s.tar.gz"
	GolandDownloadArm64Template = "https://download.jetbrains.com/go/goland-%s-aarch64.tar.gz"
)

var GolandOptions = ide.Options{
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

func NewGolandServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		GolandOptions,
		values,
		GolandProductCode,
		GolandDownloadAmd64Template,
		GolandDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "goland",
		DisplayName:   "Goland",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

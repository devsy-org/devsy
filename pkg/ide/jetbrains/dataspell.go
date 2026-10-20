package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	DataSpellProductCode           = "DS"
	DataSpellDownloadAmd64Template = "https://download.jetbrains.com/ds/dataspell-%s.tar.gz"
	DataSpellDownloadArm64Template = "https://download.jetbrains.com/ds/dataspell-%s-aarch64.tar.gz"
)

var DataSpellOptions = newProductOptions()

func NewDataSpellServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		DataSpellOptions,
		values,
		DataSpellProductCode,
		DataSpellDownloadAmd64Template,
		DataSpellDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "dataspell",
		DisplayName:   "DataSpell",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

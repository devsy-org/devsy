package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	IntellijProductCode           = "IU"
	IntellijDownloadAmd64Template = "https://download.jetbrains.com/idea/ideaIU-%s.tar.gz"
	IntellijDownloadArm64Template = "https://download.jetbrains.com/idea/ideaIU-%s-aarch64.tar.gz"
)

var IntellijOptions = newProductOptions()

func NewIntellij(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		IntellijOptions,
		values,
		IntellijProductCode,
		downloadTemplates{
			amd64: IntellijDownloadAmd64Template,
			arm64: IntellijDownloadArm64Template,
		},
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "intellij",
		DisplayName:   "Intellij",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

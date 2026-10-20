package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	PycharmProductCode           = "PY"
	PycharmDownloadAmd64Template = "https://download.jetbrains.com/python/pycharm-professional-%s.tar.gz"
	PycharmDownloadArm64Template = "https://download.jetbrains.com/python/pycharm-professional-%s-aarch64.tar.gz"
)

var PyCharmOptions = newProductOptions()

func NewPyCharmServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		PyCharmOptions,
		values,
		PycharmProductCode,
		PycharmDownloadAmd64Template,
		PycharmDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "pycharm",
		DisplayName:   "PyCharm",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

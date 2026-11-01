package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
)

const (
	RubyMineProductCode           = "RM"
	RubyMineDownloadAmd64Template = "https://download.jetbrains.com/ruby/RubyMine-%s.tar.gz"
	RubyMineDownloadArm64Template = "https://download.jetbrains.com/ruby/RubyMine-%s-aarch64.tar.gz"
)

var RubyMineOptions = newProductOptions()

func NewRubyMineServer(
	userName string,
	values map[string]config.OptionValue,
) *GenericJetBrainsServer {
	amd64Download, arm64Download := getDownloadURLs(
		RubyMineOptions,
		values,
		RubyMineProductCode,
		RubyMineDownloadAmd64Template,
		RubyMineDownloadArm64Template,
	)
	return newGenericServer(userName, &GenericOptions{
		ID:            "rubymine",
		DisplayName:   "RubyMine",
		DownloadAmd64: amd64Download,
		DownloadArm64: arm64Download,
	})
}

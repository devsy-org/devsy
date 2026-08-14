package jetbrains

import (
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
)

const (
	RubyMineProductCode           = "RM"
	RubyMineDownloadAmd64Template = "https://download.jetbrains.com/ruby/RubyMine-%s.tar.gz"
	RubyMineDownloadArm64Template = "https://download.jetbrains.com/ruby/RubyMine-%s-aarch64.tar.gz"
)

var RubyMineOptions = ide.Options{
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

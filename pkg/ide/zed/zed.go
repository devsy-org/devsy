//go:build !linux

package zed

import (
	"context"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	devsyopen "github.com/devsy-org/devsy/pkg/open"
)

// Open first finds the zed binary for the local platform and then opens the zed editor with the given workspace folder.
func Open(
	ctx context.Context,
	values map[string]config.OptionValue,
	target Target,
) error {
	log.Info("Opening Zed editor")

	openURL := buildOpenURL(target.SSHHost, target.WorkspaceFolder)
	err := devsyopen.Run(openURL)
	if err != nil {
		log.Debugf("Starting Zed caused error: %v", err)
		log.Errorf("Seems like you don't have Zed installed on your computer locally")
		return err
	}

	return nil
}

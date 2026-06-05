package devsyconfig

import (
	"fmt"
	"os/exec"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform/client"
)

func AuthDevsyCliToPlatform(config *client.Config) error {
	cmd := exec.Command( // #nosec G204 -- binary name is a compile-time constant
		pkgconfig.BinaryName,
		"pro",
		"login",
		"--access-key",
		config.AccessKey,
		config.Host,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debugf(
			"Failed executing `%s pro login`: %v, output: %s",
			pkgconfig.BinaryName,
			err,
			out,
		)
		return fmt.Errorf(
			"error executing '%s pro login' command: %w, host: %v",
			pkgconfig.BinaryName,
			err,
			config.Host,
		)
	}

	return nil
}

func AuthVClusterCliToPlatform(config *client.Config) error {
	// Check if vcluster is available inside the workspace
	if _, err := exec.LookPath("vcluster"); err != nil {
		log.Debugf("'vcluster' command is not available")
		return nil
	}

	cmd := exec.Command("vcluster", "login", "--access-key", config.AccessKey, config.Host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debugf("Failed executing `vcluster login` : %v, output: %s", err, out)
		return fmt.Errorf("error executing 'vcluster login' command: %w", err)
	}

	return nil
}

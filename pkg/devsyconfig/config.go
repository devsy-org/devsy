package devsyconfig

import (
	"context"
	"fmt"
	"os/exec"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

func AuthDevsyCliToPlatform(config *client.Config) error {
	args := []string{"pro", "login", "--access-key", config.AccessKey, config.Host}
	result, err := subprocess.Run(context.Background(), pkgconfig.BinaryName, args, subprocess.Options{
		Redactor: secrets.NewRedactor([]string{"ACCESS_KEY=" + config.AccessKey}),
	})
	if err != nil {
		log.Debugf(
			"Failed executing `%s pro login`: %v, output: %s",
			pkgconfig.BinaryName,
			err,
			result.Stderr,
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

	result, err := subprocess.Run(context.Background(), "vcluster", []string{
		"login", "--access-key", config.AccessKey, config.Host,
	}, subprocess.Options{
		Redactor: secrets.NewRedactor([]string{"ACCESS_KEY=" + config.AccessKey}),
	})
	if err != nil {
		log.Debugf("Failed executing `vcluster login` : %v, output: %s", err, result.Stderr)
		return fmt.Errorf("error executing 'vcluster login' command: %w", err)
	}

	return nil
}

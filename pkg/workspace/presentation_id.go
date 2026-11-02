package workspace

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/config"
)

type SSHConfigLocation struct {
	ConfigPath  string
	IncludePath string
}

func ValidateNewWorkspaceID(
	devsyConfig *config.Config,
	id string,
	location SSHConfigLocation,
) error {
	return checkWorkspaceIDHost(devsyConfig, id, id, location)
}

func ValidateRenameTarget(
	devsyConfig *config.Config,
	oldID, newID string,
	location SSHConfigLocation,
) error {
	return checkWorkspaceIDHost(devsyConfig, oldID, newID, location)
}

func checkWorkspaceIDHost(
	devsyConfig *config.Config,
	ownID, id string,
	location SSHConfigLocation,
) error {
	if id == "" {
		return nil
	}

	others, err := listOtherWorkspaces(devsyConfig.DefaultContext, ownID)
	if err != nil {
		return err
	}
	if err := checkAliasWorkspaceConflict(others, id); err != nil {
		return fmt.Errorf("workspace id %q: %w", id, err)
	}

	paths, err := resolveSSHPaths(location.ConfigPath, location.IncludePath)
	if err != nil {
		return err
	}
	if err := checkAliasSSHConflict(paths, ownID+config.SSHHostSuffix, id); err != nil {
		return fmt.Errorf("workspace id %q: %w", id, err)
	}

	return nil
}

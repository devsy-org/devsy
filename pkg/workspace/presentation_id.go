package workspace

import (
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
)

type SSHConfigLocation struct {
	ConfigPath  string
	IncludePath string
}

func (l SSHConfigLocation) withContextDefaults(devsyConfig *config.Config) SSHConfigLocation {
	if l.ConfigPath == "" {
		l.ConfigPath = devsyConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}
	if l.IncludePath == "" {
		l.IncludePath = devsyConfig.ContextOption(config.ContextOptionSSHConfigIncludePath)
	}

	return l
}

func ValidateNewWorkspaceID(
	devsyConfig *config.Config,
	id string,
	location SSHConfigLocation,
) error {
	unlock, err := lockPresentation()
	if err != nil {
		return err
	}
	defer unlock()

	return ValidateNewWorkspaceIDLocked(devsyConfig, id, location)
}

func ValidateRenameTarget(
	devsyConfig *config.Config,
	oldID, newID string,
	location SSHConfigLocation,
) error {
	return checkWorkspaceIDHost(devsyConfig, oldID, newID, location)
}

func ValidatePreferredSSHHost(contextName, id, preferredHost string) error {
	alias := strings.TrimSuffix(preferredHost, config.SSHHostSuffix)
	if alias == "" || strings.EqualFold(alias, id) {
		return nil
	}

	others, err := listOtherWorkspaces(contextName, id)
	if err != nil {
		return err
	}

	return checkAliasWorkspaceConflict(others, alias)
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

package up

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	MountConsistencyConsistent = "consistent"
	MountConsistencyCached     = "cached"
	MountConsistencyDelegated  = "delegated"

	UpdateRemoteUserUIDOn  = "on"
	UpdateRemoteUserUIDOff = "off"
)

//nolint:cyclop
func (cmd *UpCmd) validate() error {
	if err := validatePodmanFlags(cmd); err != nil {
		return err
	}
	if err := config2.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}
	if cmd.DefaultUserEnvProbe != "" {
		if _, err := config2.NewUserEnvProbe(cmd.DefaultUserEnvProbe); err != nil {
			return err
		}
	}
	if cmd.ExtraDevContainerPath != "" {
		absPath, err := filepath.Abs(cmd.ExtraDevContainerPath)
		if err != nil {
			return err
		}
		cmd.ExtraDevContainerPath = absPath
	}
	if cmd.WorkspaceMountConsistency != "" {
		switch cmd.WorkspaceMountConsistency {
		case MountConsistencyConsistent, MountConsistencyCached, MountConsistencyDelegated:
		default:
			return fmt.Errorf(
				"invalid --workspace-mount-consistency value %q: must be one of %s, %s, %s",
				cmd.WorkspaceMountConsistency,
				MountConsistencyConsistent, MountConsistencyCached, MountConsistencyDelegated,
			)
		}
	}
	if cmd.UpdateRemoteUserUIDDefault != "" {
		switch cmd.UpdateRemoteUserUIDDefault {
		case UpdateRemoteUserUIDOn, UpdateRemoteUserUIDOff:
		default:
			return fmt.Errorf(
				"invalid --update-remote-user-uid-default value %q: must be \"on\" or \"off\"",
				cmd.UpdateRemoteUserUIDDefault,
			)
		}
	}
	return nil
}

func validatePodmanFlags(cmd *UpCmd) error {
	if cmd.Userns != "" && (len(cmd.UidMap) > 0 || len(cmd.GidMap) > 0) {
		return fmt.Errorf(
			"--userns cannot be combined with --uidmap or --gidmap (mutually exclusive)",
		)
	}
	for _, m := range cmd.UidMap {
		if !isValidMapping(m) {
			return fmt.Errorf(
				"invalid --uidmap format: %s (expected: container_id:host_id:amount)",
				m,
			)
		}
	}
	for _, m := range cmd.GidMap {
		if !isValidMapping(m) {
			return fmt.Errorf(
				"invalid --gidmap format: %s (expected: container_id:host_id:amount)",
				m,
			)
		}
	}
	return nil
}

func isValidMapping(mapping string) bool {
	parts := strings.Split(mapping, ":")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

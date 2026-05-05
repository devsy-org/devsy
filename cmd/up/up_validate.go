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
	if err := cmd.resolveExtraDevContainerPath(); err != nil {
		return err
	}
	if err := validateWorkspaceMountConsistency(cmd.WorkspaceMountConsistency); err != nil {
		return err
	}

	return validateRemoteUserUID(cmd.UpdateRemoteUserUIDDefault)
}

func (cmd *UpCmd) resolveExtraDevContainerPath() error {
	if cmd.ExtraDevContainerPath == "" {
		return nil
	}
	absPath, err := filepath.Abs(cmd.ExtraDevContainerPath)
	if err != nil {
		return err
	}
	cmd.ExtraDevContainerPath = absPath

	return nil
}

func validateWorkspaceMountConsistency(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case MountConsistencyConsistent, MountConsistencyCached, MountConsistencyDelegated:
		return nil
	default:
		return fmt.Errorf(
			"invalid --workspace-mount-consistency value %q: must be one of %s, %s, %s",
			value,
			MountConsistencyConsistent, MountConsistencyCached, MountConsistencyDelegated,
		)
	}
}

func validateRemoteUserUID(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case UpdateRemoteUserUIDOn, UpdateRemoteUserUIDOff:
		return nil
	default:
		return fmt.Errorf(
			"invalid --update-remote-user-uid-default value %q: must be \"on\" or \"off\"",
			value,
		)
	}
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

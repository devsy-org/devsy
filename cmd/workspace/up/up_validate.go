package up

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
)

const (
	MountConsistencyConsistent = "consistent"
	MountConsistencyCached     = "cached"
	MountConsistencyDelegated  = "delegated"

	UpdateRemoteUserUIDOn  = "on"
	UpdateRemoteUserUIDOff = "off"
)

func (cmd *UpCmd) validate() error {
	if err := cmd.resolveDevContainerSource(); err != nil {
		return err
	}
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
	if err := validateMounts(cmd.Mounts); err != nil {
		return err
	}

	return validateRemoteUserUID(cmd.UpdateRemoteUserUIDDefault)
}

func (cmd *UpCmd) resolveDevContainerSource() error {
	spec, err := devcontainer.ParseSourceSpec(cmd.DevContainerSource)
	if err != nil {
		return err
	}
	if spec == nil {
		return nil
	}
	switch spec.Kind {
	case devcontainer.SourceID:
		cmd.DevContainerID = spec.ID
		cmd.DevContainerSource = ""
	case devcontainer.SourcePath:
		// An in-repo relative path is just a config path; an external absolute
		// path is left in the source so the runner imports it (bringing its
		// sibling assets — Dockerfile, features — into the workspace).
		if !filepath.IsAbs(spec.Path) {
			cmd.DevContainerPath = spec.Path
			cmd.DevContainerSource = ""
		}
	case devcontainer.SourceNone, devcontainer.SourceImage:
	}
	return nil
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

func validateMounts(mounts []string) error {
	for _, m := range mounts {
		parsed := config2.ParseMount(m)
		if parsed.Target == "" {
			return fmt.Errorf(
				"invalid %s value %q: target (dst/destination/target) is required",
				names.Flag(names.Mount),
				m,
			)
		}
	}
	return nil
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
			"invalid %s value %q: must be \"on\" or \"off\"",
			names.Flag(names.UpdateRemoteUserUID),
			value,
		)
	}
}

// validatePodmanFlags validates UID/GID mapping formats. The --userns vs
// --uidmap/--gidmap exclusivity is enforced declaratively via
// MarkFlagsMutuallyExclusive at registration.
func validatePodmanFlags(cmd *UpCmd) error {
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

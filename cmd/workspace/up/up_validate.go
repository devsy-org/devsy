package up

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/terminal"
)

const (
	MountConsistencyConsistent = "consistent"
	MountConsistencyCached     = "cached"
	MountConsistencyDelegated  = "delegated"

	UpdateRemoteUserUIDOn  = "on"
	UpdateRemoteUserUIDOff = "off"
)

func (cmd *UpCmd) validate() error {
	if err := cmd.validateInteractivePostStart(); err != nil {
		return err
	}
	cmd.applySkipLaunchIDEDefault()

	if err := devcontainer.ResolveSourceSpec(&cmd.CLIOptions); err != nil {
		return err
	}
	if err := validatePodmanFlags(cmd); err != nil {
		return err
	}
	if err := config2.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}
	if err := cmd.validateUserEnvProbe(); err != nil {
		return err
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

func (cmd *UpCmd) validateInteractivePostStart() error {
	if err := cmd.validateTerminalDimensions(); err != nil {
		return err
	}
	if !cmd.InteractivePostStart {
		return nil
	}
	if err := cmd.validateInteractiveRoute(nil); err != nil {
		return err
	}
	if err := cmd.validateInteractiveFlagConflicts(); err != nil {
		return err
	}
	return cmd.validateInteractiveTerminal()
}

func (cmd *UpCmd) validateTerminalDimensions() error {
	if cmd.TerminalRows < 0 || cmd.TerminalRows > 65535 || cmd.TerminalColumns < 0 ||
		cmd.TerminalColumns > 65535 {
		return fmt.Errorf("--terminal-rows and --terminal-columns must be between 0 and 65535")
	}
	return nil
}

func (cmd *UpCmd) validateInteractiveFlagConflicts() error {
	if cmd.Detach || cmd.Prebuild || cmd.Recovery || cmd.SkipPostStart ||
		cmd.SkipNonBlockingCommands {
		return fmt.Errorf(
			"--interactive-post-start cannot be combined with --detach, --prebuild, " +
				"--recovery, --skip-post-start, or --skip-non-blocking-commands",
		)
	}
	return nil
}

func (cmd *UpCmd) validateInteractiveTerminal() error {
	if slices.Contains(os.Environ(), "DEVSY_UI=true") {
		return fmt.Errorf(
			"--interactive-post-start requires an attached terminal and is unavailable in DEVSY_UI mode",
		)
	}
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return fmt.Errorf(
			"--interactive-post-start requires plain output; JSON result output cannot share the terminal",
		)
	}
	if err := terminal.Attached(os.Stdin, os.Stdout); err != nil {
		return fmt.Errorf(
			"--interactive-post-start requires attached stdin and stdout terminals: %w",
			err,
		)
	}
	return nil
}

// installIDE gates the container-side IDE server download on IDE name alone,
// not IDELaunch, so skipping launch without also defaulting IDE to none
// still downloads an unwanted binary.
func (cmd *UpCmd) applySkipLaunchIDEDefault() {
	if cmd.IDELaunch == opener.LaunchSkip && cmd.IDE == "" {
		cmd.IDE = string(config.IDENone)
	}
}

func (cmd *UpCmd) validateUserEnvProbe() error {
	if cmd.DefaultUserEnvProbe == "" {
		return nil
	}
	if _, err := config2.NewUserEnvProbe(cmd.DefaultUserEnvProbe); err != nil {
		return err
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

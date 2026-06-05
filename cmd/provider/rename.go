package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// RenameCmd implements the provider rename command.
type RenameCmd struct {
	*flags.GlobalFlags
}

// NewRenameCmd creates a new command for renaming a provider.
func NewRenameCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &RenameCmd{
		GlobalFlags: globalFlags,
	}

	return &cobra.Command{
		Use:     "rename <current-name> <new-name>",
		Aliases: []string{"mv"},
		Short:   "Rename a provider",
		Args:    cobra.ExactArgs(2),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}
}

// Run validates inputs, loads config, and executes the provider rename.
func (cmd *RenameCmd) Run(ctx context.Context, args []string) error {
	oldName, newName := args[0], args[1]

	if oldName == newName {
		return fmt.Errorf("new name is the same as the current name")
	}

	if err := validateProviderName(newName); err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	if err := validateProviderRename(devsyConfig, oldName); err != nil {
		return err
	}

	if devsyConfig.Current().Providers[newName] != nil {
		return fmt.Errorf("provider %s already exists", newName)
	}

	return renameProvider(ctx, devsyConfig, oldName, newName)
}

// validateProviderName checks that the given name is non-empty, matches the
// allowed character set (lowercase letters, numbers, dashes), and does not
// exceed the maximum length of 32 characters.
func validateProviderName(newName string) error {
	if strings.TrimSpace(newName) == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if provider.ProviderNameRegEx.MatchString(newName) {
		return fmt.Errorf("provider name can only include lowercase letters, numbers or dashes")
	}
	if len(newName) > 32 {
		return fmt.Errorf("provider name cannot be longer than 32 characters")
	}
	return nil
}

// getWorkspacesForProvider returns all local workspaces whose provider matches
// the given name.
func getWorkspacesForProvider(
	devsyConfig *config.Config,
	providerName string,
) ([]*provider.Workspace, error) {
	workspaces, err := workspace.ListLocalWorkspaces(
		devsyConfig.DefaultContext,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	var matched []*provider.Workspace
	for _, ws := range workspaces {
		if ws.Provider.Name == providerName {
			matched = append(matched, ws)
		}
	}
	return matched, nil
}

// getMachinesForProvider returns all machines whose provider matches the given
// name.
func getMachinesForProvider(
	devsyConfig *config.Config,
	providerName string,
) ([]*provider.Machine, error) {
	machines, err := workspace.ListMachines(devsyConfig)
	if err != nil {
		return nil, fmt.Errorf("listing machines: %w", err)
	}
	var matched []*provider.Machine
	for _, m := range machines {
		if m.Provider.Name == providerName {
			matched = append(matched, m)
		}
	}
	return matched, nil
}

// switchWorkspaces updates each workspace to reference the new provider name.
// It stops on the first failure and returns the successfully switched
// workspaces so the caller can roll them back.
func switchWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	workspaces []*provider.Workspace,
	newName string,
) ([]*provider.Workspace, error) {
	var switched []*provider.Workspace
	for _, ws := range workspaces {
		if err := workspace.SwitchProvider(ctx, devsyConfig, ws, newName); err != nil {
			return switched, fmt.Errorf("failed to switch workspace %s: %w", ws.ID, err)
		}
		switched = append(switched, ws)
	}
	return switched, nil
}

// switchMachines updates each machine to reference the new provider name.
// It stops on the first failure and returns the successfully switched
// machines so the caller can roll them back.
func switchMachines(machines []*provider.Machine, newName string) ([]*provider.Machine, error) {
	var switched []*provider.Machine
	for _, m := range machines {
		oldName := m.Provider.Name
		m.Provider.Name = newName
		if err := provider.SaveMachineConfig(m); err != nil {
			m.Provider.Name = oldName
			return switched, fmt.Errorf("failed to switch machine %s: %w", m.ID, err)
		}
		switched = append(switched, m)
	}
	return switched, nil
}

// setDefaultProvider updates the default provider setting if it currently
// points to oldName. Returns true if the default was changed.
func setDefaultProvider(devsyConfig *config.Config, oldName, newName string) (bool, error) {
	if devsyConfig.Current().DefaultProvider != oldName {
		return false, nil
	}
	devsyConfig.Current().DefaultProvider = newName
	if err := config.SaveConfig(devsyConfig); err != nil {
		devsyConfig.Current().DefaultProvider = oldName
		return false, err
	}
	return true, nil
}

// renameState tracks the mutations performed during a rename so they can be
// undone if a later step fails.
type renameState struct {
	devsyConfig        *config.Config
	switchedWorkspaces []*provider.Workspace
	switchedMachines   []*provider.Machine
	defaultChanged     bool
	oldName, newName   string
}

// restoreProviderState reverts all recorded mutations in reverse order: default provider,
// workspaces, machines, and finally the provider directory move.
func (r *renameState) restoreProviderState(ctx context.Context) error {
	log.Info("rolling back changes")
	var errs error

	if r.defaultChanged {
		r.devsyConfig.Current().DefaultProvider = r.oldName
		if err := config.SaveConfig(r.devsyConfig); err != nil {
			errs = errors.Join(errs, fmt.Errorf("rollback default provider: %w", err))
		}
	}

	_, err := switchWorkspaces(ctx, r.devsyConfig, r.switchedWorkspaces, r.oldName)
	errs = errors.Join(errs, err)

	_, err = switchMachines(r.switchedMachines, r.oldName)
	errs = errors.Join(errs, err)

	if moveErr := provider.MoveProvider(r.devsyConfig, r.newName, r.oldName); moveErr != nil {
		errs = errors.Join(errs, fmt.Errorf("rollback move provider: %w", moveErr))
	}

	return errs
}

// validateProviderRename verifies that the provider exists, is not a pro
// provider, is not backing a pro instance, and has configuration state.
func validateProviderRename(devsyConfig *config.Config, oldName string) error {
	providerWithOptions, err := workspace.FindProvider(devsyConfig, oldName)
	if err != nil {
		return fmt.Errorf("provider %s not found", oldName)
	}

	if providerWithOptions.Config.IsProxyProvider() ||
		providerWithOptions.Config.IsDaemonProvider() {
		return fmt.Errorf("cannot rename a pro provider; pro providers are managed by the platform")
	}

	proInstances, err := workspace.ListProInstances(devsyConfig)
	if err != nil {
		return fmt.Errorf("listing pro instances: %w", err)
	}
	for _, inst := range proInstances {
		if inst.Provider == oldName {
			return fmt.Errorf(
				"cannot rename provider %s: it is used by pro instance %s",
				oldName,
				inst.Host,
			)
		}
	}

	if devsyConfig.Current().Providers[oldName] == nil {
		return fmt.Errorf("provider %s has no configuration state", oldName)
	}

	return nil
}

// renameProvider performs the rename: moves the provider directory, switches all
// associated workspaces and machines, and adjusts the default provider. If any
// step fails the entire operation is rolled back.
func renameProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	oldName, newName string,
) error {
	workspaces, err := getWorkspacesForProvider(devsyConfig, oldName)
	if err != nil {
		return err
	}

	machines, err := getMachinesForProvider(devsyConfig, oldName)
	if err != nil {
		return err
	}

	if err := provider.MoveProvider(devsyConfig, oldName, newName); err != nil {
		return fmt.Errorf("moving provider: %w", err)
	}

	rb := &renameState{devsyConfig: devsyConfig, oldName: oldName, newName: newName}

	rb.switchedWorkspaces, err = switchWorkspaces(ctx, devsyConfig, workspaces, newName)
	if err != nil {
		return errors.Join(err, rb.restoreProviderState(ctx))
	}

	rb.switchedMachines, err = switchMachines(machines, newName)
	if err != nil {
		return errors.Join(err, rb.restoreProviderState(ctx))
	}

	rb.defaultChanged, err = setDefaultProvider(devsyConfig, oldName, newName)
	if err != nil {
		return errors.Join(err, rb.restoreProviderState(ctx))
	}

	log.Infof("renamed provider %s to %s", oldName, newName)
	return nil
}

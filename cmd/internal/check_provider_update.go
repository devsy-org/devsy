package cmdinternal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

var errProviderNotFound = errors.New("provider not found")

type CheckProviderUpdateCmd struct {
	*flags.GlobalFlags
}

type providerVersionCheck struct {
	UpdateAvailable bool   `json:"updateAvailable"`
	LatestVersion   string `json:"latestVersion,omitempty"`
}

// NewCheckProviderUpdateCmd creates a new command.
func NewCheckProviderUpdateCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &CheckProviderUpdateCmd{
		GlobalFlags: flags,
	}
	shellCmd := &cobra.Command{
		Use:   "check-provider-update",
		Short: "Check if a provider update is available",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(cobraCmd.Context(), devsyConfig, args)
		},
	}

	return shellCmd
}

func (cmd *CheckProviderUpdateCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) error {
	if len(args) != 1 {
		return fmt.Errorf("provider is missing")
	}
	providerName := args[0]

	providerSourceRaw, currentVersion, err := loadCurrentProvider(devsyConfig, providerName)
	if err != nil {
		return err
	}

	latestProviderConfig, err := loadLatestProvider(ctx, providerSourceRaw)
	if err != nil {
		return err
	}

	versionCheck, err := resolveProviderVersions(currentVersion, latestProviderConfig.Version)
	if err != nil {
		return err
	}

	out, err := json.Marshal(versionCheck)
	if err != nil {
		return err
	}
	fmt.Println(string(out)) //nolint:forbidigo // CLI stdout output

	return nil
}

func loadCurrentProvider(
	devsyConfig *config.Config,
	providerName string,
) (source string, currentVersion string, err error) {
	source, err = workspace.ResolveProviderSource(devsyConfig, providerName)
	if err != nil {
		return "", "", fmt.Errorf("provider %s doesn't exist", providerName)
	}

	allProviders, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return "", "", err
	}
	currentProvider, ok := allProviders[providerName]
	if !ok {
		return "", "", errProviderNotFound
	}

	return source, currentProvider.Config.Version, nil
}

func resolveProviderVersions(
	current, latest string,
) (providerVersionCheck, error) {
	currentVersion, err := semver.Parse(strings.TrimPrefix(current, "v"))
	if err != nil {
		return providerVersionCheck{}, err
	}
	latestVersion, err := semver.Parse(strings.TrimPrefix(latest, "v"))
	if err != nil {
		return providerVersionCheck{}, err
	}

	versionCheck := providerVersionCheck{UpdateAvailable: false}
	if latestVersion.GT(currentVersion) {
		versionCheck.UpdateAvailable = true
		versionCheck.LatestVersion = latest
	}

	return versionCheck, nil
}

func loadLatestProvider(
	ctx context.Context,
	providerSourceRaw string,
) (*provider.ProviderConfig, error) {
	providerRaw, _, err := provider.ResolveProvider(ctx, providerSourceRaw)
	if err != nil {
		return nil, fmt.Errorf("resolve provider: %w", err)
	}
	providerConfig, err := provider.ParseProvider(bytes.NewReader(providerRaw))
	if err != nil {
		return nil, fmt.Errorf("parse provider: %w", err)
	}

	return providerConfig, nil
}

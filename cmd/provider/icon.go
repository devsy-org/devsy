package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/spf13/cobra"
)

const (
	iconThemeLight = "light"
	iconThemeDark  = "dark"
)

// IconCmd holds the icon cmd flags.
type IconCmd struct {
	*flags.GlobalFlags

	Theme string
}

// NewIconCmd creates a new command.
func NewIconCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &IconCmd{
		GlobalFlags: flags,
	}
	iconCmd := &cobra.Command{
		Use:   "icon [name]",
		Short: "Print an installed provider's cached icon as JSON",
		Long: "Reads the icon cached for an installed provider. The result is always JSON: " +
			"{revision, mimeType, dataBase64} for an image or {revision, missing: true} " +
			"when the provider has no usable image for the theme. Never uses the network.",
		Args:        cobra.ExactArgs(1),
		Annotations: telemetry.SkipInUIAnnotation(),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmd.Context != "" {
				if err := config.ValidateContextPathName(cmd.Context); err != nil {
					return fmt.Errorf("invalid provider icon context: %w", err)
				}
			}
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			return cmd.Run(devsyConfig, args[0], os.Stdout)
		},
	}

	cliflags.Add(
		iconCmd,
		cliflags.String(&cmd.Theme, names.Theme, iconThemeLight, "Icon variant: light or dark"),
	)

	return iconCmd
}

// Run prints the cached icon of the installed provider for the selected theme.
func (cmd *IconCmd) Run(devsyConfig *config.Config, providerName string, out io.Writer) error {
	if err := cmd.validate(devsyConfig.DefaultContext, providerName); err != nil {
		return err
	}

	providerConfig, err := provider.LoadProviderConfig(devsyConfig.DefaultContext, providerName)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("provider with name %s not found", providerName)
	}
	if err != nil {
		return fmt.Errorf("load provider %s: %w", providerName, err)
	}

	result, err := provider.ReadProviderIcon(devsyConfig.DefaultContext, providerConfig, cmd.Theme)
	if err != nil {
		return fmt.Errorf("read icon of provider %s: %w", providerName, err)
	}

	return json.NewEncoder(out).Encode(result)
}

func (cmd *IconCmd) validate(defaultContext, providerName string) error {
	if err := config.ValidateContextPathName(defaultContext); err != nil {
		return fmt.Errorf("invalid provider icon context: %w", err)
	}
	if err := validateProviderName(providerName); err != nil {
		return err
	}
	if cmd.Theme != iconThemeLight && cmd.Theme != iconThemeDark {
		return fmt.Errorf("unexpected theme %q, choose light or dark", cmd.Theme)
	}
	// The icon is a data contract for machine consumers, but an invalid
	// format must still fail like it does for every other command.
	if _, err := output.ResolveMode(cmd.ResultFormat); err != nil {
		return err
	}
	return nil
}

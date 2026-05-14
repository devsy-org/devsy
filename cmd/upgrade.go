package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// UpgradeCmd upgrades devcontainer feature versions to the latest available.
type UpgradeCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	Config          string
	DryRun          bool
}

// NewUpgradeCmd creates a new upgrade command.
func NewUpgradeCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpgradeCmd{GlobalFlags: f}
	upgradeCmd := &cobra.Command{
		Use:   "upgrade [feature...]",
		Short: "Upgrades devcontainer feature versions to the latest available",
		Long: `Upgrades devcontainer feature versions in devcontainer.json to the latest
available versions. If specific features are provided as arguments, only those
features are upgraded. Otherwise, all outdated features are upgraded.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	upgradeCmd.Flags().
		StringVar(&cmd.WorkspaceFolder, "workspace-folder", "", "Path to the workspace folder")
	upgradeCmd.Flags().
		StringVar(&cmd.Config, "config", "", "Path to a specific devcontainer.json")
	upgradeCmd.Flags().
		BoolVar(&cmd.DryRun, "dry-run", false, "Preview upgrades without applying them")

	return upgradeCmd
}

// Run runs the command logic.
func (cmd *UpgradeCmd) Run(_ context.Context, targets []string) error {
	parsedConfig, err := cmd.loadConfig()
	if err != nil {
		return err
	}

	if len(parsedConfig.Features) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, noFeaturesMessage)
		return nil
	}

	outdated := cmd.findUpgradeable(parsedConfig.Features, targets)
	if len(outdated) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, allUpToDateMessage)
		return nil
	}

	if cmd.DryRun {
		printOutdatedTable(outdated)
		return nil
	}

	return cmd.applyUpgrades(parsedConfig.Origin, outdated)
}

func (cmd *UpgradeCmd) loadConfig() (*devconfig.DevContainerConfig, error) {
	loader := &OutdatedCmd{
		GlobalFlags:     cmd.GlobalFlags,
		WorkspaceFolder: cmd.WorkspaceFolder,
		Config:          cmd.Config,
	}
	return loader.loadConfig()
}

func (cmd *UpgradeCmd) findUpgradeable(features map[string]any, targets []string) []outdatedEntry {
	targetSet := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetSet[normalizeFeatureRef(t)] = true
	}

	var results []outdatedEntry
	for featureID := range features {
		if len(targetSet) > 0 && !matchesTarget(featureID, targetSet) {
			continue
		}

		entry, ok := checkFeatureVersion(featureID)
		if ok {
			results = append(results, entry)
		}
	}
	return results
}

// normalizeFeatureRef strips the tag from a feature reference for matching.
func normalizeFeatureRef(ref string) string {
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		candidate := ref[:idx]
		if strings.Contains(candidate, "/") {
			return candidate
		}
	}
	return ref
}

// matchesTarget checks if a feature ID matches any of the target refs.
func matchesTarget(featureID string, targets map[string]bool) bool {
	normalized := normalizeFeatureRef(featureID)
	return targets[normalized] || targets[featureID]
}

func (cmd *UpgradeCmd) applyUpgrades(configPath string, outdated []outdatedEntry) error {
	//nolint:gosec // G304 -- configPath is from parsed devcontainer config
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	content := string(raw)
	for _, entry := range outdated {
		old := entry.repo + ":" + entry.current
		updated := entry.repo + ":" + entry.latest
		content = strings.ReplaceAll(content, old, updated)
		log.Infof("Upgraded %s: %s → %s", entry.repo, entry.current, entry.latest)
	}

	//nolint:gosec // G306 -- matching existing file permissions in the codebase
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Upgraded %d feature(s).\n", len(outdated))
	return nil
}

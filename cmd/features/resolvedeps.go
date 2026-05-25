package features

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

type ResolveDepsCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	Config          string
	Output          string
}

type resolvedFeature struct {
	ID            string         `json:"id"`
	Version       string         `json:"version,omitempty"`
	Dependencies  []string       `json:"dependencies,omitempty"`
	InstallsAfter []string       `json:"installsAfter,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
}

func NewResolveDepsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ResolveDepsCmd{GlobalFlags: globalFlags}
	resolveDepsCmd := &cobra.Command{
		Use:   "resolve-dependencies",
		Short: "Resolve feature install order from a devcontainer.json",
		Long: `Read a devcontainer.json and output the resolved feature
install order based on dependency declarations and install ordering.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return cmd.Run()
		},
	}

	resolveDepsCmd.Flags().StringVar(
		&cmd.WorkspaceFolder, "workspace-folder", "",
		"Path to workspace containing devcontainer.json",
	)
	resolveDepsCmd.Flags().StringVar(
		&cmd.Config, "config", "",
		"Path to specific devcontainer.json (optional)",
	)
	resolveDepsCmd.Flags().StringVar(
		&cmd.Output, "output", "text", "Output format (text or json)",
	)
	_ = resolveDepsCmd.MarkFlagRequired("workspace-folder")

	return resolveDepsCmd
}

func (cmd *ResolveDepsCmd) Run() error {
	if err := validateOutputFormat(cmd.Output); err != nil {
		return err
	}

	devContainerConfig, err := cmd.loadConfig()
	if err != nil {
		return fmt.Errorf("load devcontainer config: %w", err)
	}

	if devContainerConfig == nil {
		return fmt.Errorf(
			"no devcontainer.json found in workspace %q", cmd.WorkspaceFolder,
		)
	}

	if len(devContainerConfig.Features) == 0 {
		return cmd.printEmpty()
	}

	sorted, err := feature.ResolveFeatureOrder(devContainerConfig)
	if err != nil {
		return fmt.Errorf("resolve feature order: %w", err)
	}

	resolved := buildResolvedList(sorted)

	if cmd.Output == outputJSON {
		return writeJSON(os.Stdout, resolved)
	}
	return cmd.printText(resolved)
}

func (cmd *ResolveDepsCmd) printEmpty() error {
	if cmd.Output == outputJSON {
		_, err := fmt.Fprintln(os.Stdout, "[]")
		return err
	}
	_, err := fmt.Fprintln(os.Stdout, "No features declared in devcontainer.json")
	return err
}

func buildResolvedList(sorted []*config.FeatureSet) []resolvedFeature {
	resolved := make([]resolvedFeature, 0, len(sorted))
	for _, fs := range sorted {
		rf := resolvedFeature{
			ID:      fs.ConfigID,
			Version: fs.Version,
		}
		if fs.Config != nil {
			for dep := range fs.Config.DependsOn {
				rf.Dependencies = append(rf.Dependencies, dep)
			}
			rf.InstallsAfter = fs.Config.InstallsAfter
		}
		if opts, ok := fs.Options.(map[string]any); ok {
			rf.Options = opts
		}
		resolved = append(resolved, rf)
	}
	return resolved
}

func (cmd *ResolveDepsCmd) loadConfig() (*config.DevContainerConfig, error) {
	if cmd.Config != "" {
		return config.ParseDevContainerJSONFile(cmd.Config)
	}

	absPath, err := filepath.Abs(cmd.WorkspaceFolder)
	if err != nil {
		return nil, err
	}

	return config.ParseDevContainerJSON(absPath, "")
}

func (cmd *ResolveDepsCmd) printText(resolved []resolvedFeature) error {
	_, _ = fmt.Fprintf(os.Stdout, "Feature install order (%d features):\n\n", len(resolved))
	headers := []string{"#", headerFeature, "Depends On", "Installs After"}
	var rows [][]string
	for i, rf := range resolved {
		version := rf.ID
		if rf.Version != "" {
			version = rf.ID + ":" + rf.Version
		}
		deps := strings.Join(rf.Dependencies, ", ")
		after := strings.Join(rf.InstallsAfter, ", ")
		rows = append(rows, []string{fmt.Sprintf("%d", i+1), version, deps, after})
	}
	table.Print(headers, rows)
	return nil
}

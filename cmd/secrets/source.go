package secrets

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	secrets2 "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

type addSOPSSourceOptions struct {
	out         io.Writer
	globalFlags *flags.GlobalFlags
	name        string
	filePath    string
	format      string
}

func NewSourceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Manage external secret sources",
	}
	cmd.AddCommand(newSourceAddCmd(globalFlags))
	cmd.AddCommand(newSourceListCmd(globalFlags))
	cmd.AddCommand(newSourceRemoveCmd(globalFlags))
	return cmd
}

func newSourceAddCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "add sops NAME PATH",
		Short: "Register a SOPS-encrypted file as a secret source",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != secrets2.SOPSFormatter {
				return fmt.Errorf("unsupported secret source type %q", args[0])
			}
			return addSOPSSource(cmd.Context(), addSOPSSourceOptions{
				out:         cmd.OutOrStdout(),
				globalFlags: globalFlags,
				name:        args[1],
				filePath:    args[2],
				format:      format,
			})
		},
	}
	cmd.Flags().StringVar(
		&format,
		"format",
		"",
		"SOPS document format override (yaml, json, dotenv)",
	)
	return cmd
}

func addSOPSSource(ctx context.Context, opts addSOPSSourceOptions) error {
	if err := secrets2.ValidateSourceName(opts.name); err != nil {
		return err
	}
	if opts.name == secrets2.LocalSourceName {
		return fmt.Errorf("secret source name %q is reserved", opts.name)
	}
	absolutePath, err := resolveSOPSFile(opts.filePath)
	if err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(opts.globalFlags.Context, opts.globalFlags.Provider)
	if err != nil {
		return err
	}
	if err := secrets2.NewSOPSSource(opts.name, absolutePath, opts.format).
		Validate(ctx); err != nil {
		return fmt.Errorf("cannot add SOPS source %q: %w", opts.name, err)
	}
	if err := persistSOPSSource(devsyConfig, opts.name, absolutePath, opts.format); err != nil {
		return err
	}
	_, err = fmt.Fprintf(opts.out, "Added SOPS secret source %q: %s\n", opts.name, absolutePath)
	return err
}

func resolveSOPSFile(filePath string) (string, error) {
	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	absolutePath, err = filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return "", fmt.Errorf("resolve SOPS source path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("stat SOPS source path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("SOPS source path %q is not a regular file", filePath)
	}
	return absolutePath, nil
}

func persistSOPSSource(devsyConfig *config.Config, name, filePath, format string) error {
	sources, err := secrets2.LoadSourceConfigs(devsyConfig)
	if err != nil {
		return err
	}
	sources, err = secrets2.AddSourceConfig(sources, secrets2.SourceConfig{
		Name:   name,
		Type:   secrets2.SOPSFormatter,
		Path:   filePath,
		Format: format,
	})
	if err != nil {
		return err
	}
	return secrets2.SaveSourceConfigs(devsyConfig, sources)
}

func newSourceListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured external secret sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
			if err != nil {
				return err
			}
			sources, err := secrets2.LoadSourceConfigs(devsyConfig)
			if err != nil {
				return err
			}
			sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
			return writeSourceList(cmd.OutOrStdout(), sources)
		},
	}
}

func writeSourceList(out io.Writer, sources []secrets2.SourceConfig) error {
	if len(sources) == 0 {
		_, err := fmt.Fprintln(out, "No external secret sources configured.")
		return err
	}
	if _, err := fmt.Fprintln(out, "NAME\tTYPE\tLOCATION"); err != nil {
		return err
	}
	for _, source := range sources {
		if _, err := fmt.Fprintf(
			out,
			"%s\t%s\t%s\n",
			source.Name,
			source.Type,
			source.Path,
		); err != nil {
			return err
		}
	}
	return nil
}

func newSourceRemoveCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove an external secret source reference",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return removeSource(cmd.OutOrStdout(), globalFlags, args[0])
		},
	}
}

func removeSource(out io.Writer, globalFlags *flags.GlobalFlags, name string) error {
	devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
	if err != nil {
		return err
	}
	if references := attachedSourceReferences(devsyConfig, name); len(references) > 0 {
		return fmt.Errorf(
			"cannot remove secret source %q: attached secrets still reference it: %v",
			name,
			references,
		)
	}
	if err := removeSourceConfig(devsyConfig, name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Removed secret source %q\n", name)
	return err
}

func attachedSourceReferences(devsyConfig *config.Config, name string) []string {
	current := devsyConfig.Current()
	if current == nil {
		return nil
	}
	var references []string
	for _, bound := range current.Secrets {
		ref, err := secrets2.ParseRef(bound)
		if err == nil && ref.Source == name {
			references = append(references, bound)
		}
	}
	return references
}

func removeSourceConfig(devsyConfig *config.Config, name string) error {
	sources, err := secrets2.LoadSourceConfigs(devsyConfig)
	if err != nil {
		return err
	}
	sources, removed := secrets2.RemoveSourceConfig(sources, name)
	if !removed {
		return fmt.Errorf("secret source %q is not configured", name)
	}
	return secrets2.SaveSourceConfigs(devsyConfig, sources)
}

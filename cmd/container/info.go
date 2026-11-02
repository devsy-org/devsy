package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

type containerInfo struct {
	SSHCommand string `json:"sshCommand"`
	SSHHost    string `json:"sshHost"`
	RemoteUser string `json:"remoteUser"`
}

func newInfoCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show this container's SSH connection information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := output.ResolveMode(globalFlags.ResultFormat)
			if err != nil {
				return err
			}
			info, err := readContainerInfo(cmd.Context())
			if err != nil {
				classified := clierr.Classify(err)
				if classified.Code == clierr.CodeUnknown {
					classified.Code = "container_info_failed"
				}
				return classified
			}
			return writeContainerInfo(cmd.OutOrStdout(), mode, info)
		},
	}
}

func readContainerInfo(ctx context.Context) (*containerInfo, error) {
	workspaceID := os.Getenv(config.EnvWorkspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf(
			"%s is missing; Devsy workspace identity is unavailable here. "+
				"recreate Docker Compose containers created before it was injected",
			config.EnvWorkspaceID,
		)
	}
	result, err := readInfoSetupResult(ctx)
	if err != nil {
		return nil, err
	}
	info := &containerInfo{
		SSHHost:    workspaceID + config.SSHHostSuffix,
		RemoteUser: devconfig.GetRemoteUser(result),
	}
	info.SSHCommand = "ssh " + shellescape.Quote(info.RemoteUser+"@"+info.SSHHost)
	return info, nil
}

func readInfoSetupResult(ctx context.Context) (*devconfig.Result, error) {
	command := exec.CommandContext(ctx, "sh")
	command.Args = append(command.Args, "-c", config.ReadDevContainerResultCommand())
	raw, err := command.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf(
				"read selected container setup result: %w: %s",
				err,
				strings.TrimSpace(string(exit.Stderr)),
			)
		}
		return nil, fmt.Errorf("read selected container setup result: %w", err)
	}
	result := &devconfig.Result{}
	if err := json.Unmarshal(raw, result); err != nil {
		return nil, fmt.Errorf("parse container setup result: %w", err)
	}
	if result.MergedConfig == nil || result.ContainerDetails == nil || result.Error != "" {
		return nil, fmt.Errorf("container setup result is incomplete or failed")
	}
	return result, nil
}

func writeContainerInfo(w io.Writer, mode string, info *containerInfo) error {
	if mode == output.ModeJSON {
		return json.NewEncoder(w).Encode(info)
	}
	rows := [][]string{
		{"SSH command", info.SSHCommand},
		{"SSH host", info.SSHHost},
		{"Remote user", info.RemoteUser},
	}
	_, err := fmt.Fprintln(w, table.Render([]string{"Field", "Value"}, rows))
	return err
}

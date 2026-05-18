package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

const (
	cleanVolumePrefix    = "devsy-agent-"
	cleanVolumeMountPath = "/opt/devsy"
	cleanBinaryName      = "devsy"
	cleanHelperImage     = "busybox:latest"
)

// CleanCmd holds the cmd flags.
type CleanCmd struct {
	*flags.GlobalFlags

	DockerCommand string
	HelperImage   string
}

// NewCleanCmd creates a new command.
func NewCleanCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CleanCmd{
		GlobalFlags: globalFlags,
	}
	cleanCmd := &cobra.Command{
		Use:   "clean [workspace-id]",
		Short: "Removes the agent binary from the Docker volume for a workspace",
		Long: `Removes the agent binary from the Docker named volume for the specified workspace.
This forces a fresh binary injection on the next workspace start.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}
	cleanCmd.Flags().
		StringVar(&cmd.DockerCommand, "docker-command", "docker", "Docker command to use")
	cleanCmd.Flags().
		StringVar(&cmd.HelperImage, "helper-image", cleanHelperImage, "Helper image for volume operations")
	return cleanCmd
}

func (cmd *CleanCmd) Run(ctx context.Context, workspaceID string) error {
	if workspaceID == "" {
		return fmt.Errorf("workspace ID must not be empty")
	}

	volumeName := cleanVolumePrefix + workspaceID
	log.Infof("Removing agent binary from volume %s", volumeName)

	if err := cmd.removeBinaryFromVolume(ctx, volumeName); err != nil {
		return fmt.Errorf("remove agent binary from volume %s: %w", volumeName, err)
	}

	log.Infof("Successfully removed agent binary from volume %s", volumeName)
	return nil
}

func (cmd *CleanCmd) removeBinaryFromVolume(ctx context.Context, volumeName string) error {
	if err := cmd.checkVolumeExists(ctx, volumeName); err != nil {
		return err
	}

	binaryPath := cleanVolumeMountPath + "/" + cleanBinaryName
	script := fmt.Sprintf(`rm -f "%s"`, binaryPath)
	args := []string{
		"run", "--rm",
		"-v", volumeName + ":" + cleanVolumeMountPath,
		cmd.helperImage(),
		"sh", "-c", script,
	}

	out, err := cmd.dockerCmd(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (cmd *CleanCmd) checkVolumeExists(ctx context.Context, volumeName string) error {
	out, err := cmd.dockerCmd(ctx, "volume", "inspect", volumeName).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"volume %s not found (is Docker running?): %s: %w",
			volumeName,
			strings.TrimSpace(string(out)),
			err,
		)
	}
	return nil
}

func (cmd *CleanCmd) dockerCmd(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- args are constructed internally, not from user input
	return exec.CommandContext(ctx, cmd.dockerCommand(), args...)
}

func (cmd *CleanCmd) dockerCommand() string {
	if cmd.DockerCommand != "" {
		return cmd.DockerCommand
	}
	return "docker"
}

func (cmd *CleanCmd) helperImage() string {
	if cmd.HelperImage != "" {
		return cmd.HelperImage
	}
	return cleanHelperImage
}

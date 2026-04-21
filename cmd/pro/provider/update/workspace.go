package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/form"
	"github.com/devsy-org/devsy/pkg/platform/project"
	"github.com/devsy-org/devsy/pkg/terminal"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceCmd holds the cmd flags.
type WorkspaceCmd struct {
	*flags.GlobalFlags
}

// NewWorkspaceCmd creates a new command.
func NewWorkspaceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &WorkspaceCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "workspace",
		Short:  "Create a workspace",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), os.Stdin, os.Stdout, os.Stderr)
		},
	}

	return c
}

func (cmd *WorkspaceCmd) Run(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	// GUI
	instanceEnv := os.Getenv(platform.WorkspaceInstanceEnv)
	if instanceEnv != "" {
		newInstance := &managementv1.DevsyWorkspaceInstance{}
		err := json.Unmarshal([]byte(instanceEnv), newInstance)
		if err != nil {
			return fmt.Errorf("unmarshal workspace instance %s: %w", instanceEnv, err)
		}
		newInstance.TypeMeta = metav1.TypeMeta{} // ignore

		projectName := project.ProjectFromNamespace(newInstance.GetNamespace())
		opts := platform.FindInstanceOptions{Name: newInstance.GetName(), ProjectName: projectName}
		oldInstance, err := platform.FindInstance(ctx, baseClient, opts)
		if err != nil {
			return err
		}
		if oldInstance == nil {
			return fmt.Errorf(
				"workspace instance %q not found in project %q",
				newInstance.GetName(),
				projectName,
			)
		}

		updatedInstance, err := updateInstance(ctx, baseClient, oldInstance, newInstance)
		if err != nil {
			return err
		}

		out, err := json.Marshal(updatedInstance)
		if err != nil {
			return err
		}
		fmt.Println(string(out))

		return nil
	}

	// CLI
	if !terminal.IsTerminalIn {
		return fmt.Errorf("unable to update instance through CLI if stdin is not a terminal")
	}
	workspaceID := os.Getenv(platform.WorkspaceIDEnv)
	workspaceUID := os.Getenv(platform.WorkspaceUIDEnv)
	project := os.Getenv(platform.ProjectEnv)
	if workspaceUID == "" || workspaceID == "" || project == "" {
		return fmt.Errorf(
			"workspaceID, workspaceUID or project not found: %s, %s, %s",
			workspaceID,
			workspaceUID,
			project,
		)
	}

	opts := platform.FindInstanceOptions{UID: workspaceUID, ProjectName: project}
	oldInstance, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	}
	if oldInstance == nil {
		return fmt.Errorf(
			"workspace instance with UID %q not found in project %q",
			workspaceUID,
			project,
		)
	}

	newInstance, err := form.UpdateInstance(ctx, baseClient, oldInstance)
	if err != nil {
		return err
	}

	_, err = updateInstance(ctx, baseClient, oldInstance, newInstance)
	if err != nil {
		return err
	}

	return nil
}

func updateInstance(
	ctx context.Context,
	client client.Client,
	oldInstance *managementv1.DevsyWorkspaceInstance,
	newInstance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	// This ensures the template is kept up to date with configuration changes
	if newInstance.Spec.TemplateRef != nil {
		newInstance.Spec.TemplateRef.SyncOnce = true
	}

	return platform.UpdateInstance(ctx, client, oldInstance, newInstance)
}

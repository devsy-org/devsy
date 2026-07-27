package create

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/form"
	"github.com/devsy-org/devsy/pkg/platform/project"
	"github.com/devsy-org/devsy/pkg/provider"
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

	// fully serialized instance, right now only used by GUI
	if instanceEnv := os.Getenv(platform.WorkspaceInstanceEnv); instanceEnv != "" {
		return createFromInstanceEnv(ctx, baseClient, instanceEnv)
	}

	// Info through env, right now only used by CLI
	return createFromEnv(ctx, baseClient)
}

func createFromInstanceEnv(
	ctx context.Context,
	baseClient client.Client,
	instanceEnv string,
) error {
	instance := &managementv1.DevsyWorkspaceInstance{} // init pointer
	err := json.Unmarshal([]byte(instanceEnv), instance)
	if err != nil {
		return fmt.Errorf("unmarshal workspace instance %s: %w", instanceEnv, err)
	}

	updatedInstance, err := createInstance(ctx, baseClient, instance)
	if err != nil {
		return err
	}

	out, err := json.Marshal(updatedInstance)
	if err != nil {
		return err
	}

	fmt.Println(string(out)) //nolint:forbidigo // CLI stdout output
	return nil
}

type workspaceEnv struct {
	id      string
	uid     string
	folder  string
	context string
	picture string
	source  string
}

func readWorkspaceEnv() (workspaceEnv, error) {
	env := workspaceEnv{
		id:      os.Getenv(config.EnvProviderWorkspaceID),
		uid:     os.Getenv(config.EnvProviderWorkspaceUID),
		folder:  os.Getenv(config.EnvProviderWorkspaceFolder),
		context: os.Getenv(config.EnvProviderWorkspaceContext),
		picture: os.Getenv(platform.WorkspacePictureEnv),
		source:  os.Getenv(platform.WorkspaceSourceEnv),
	}
	if env.uid == "" || env.id == "" || env.folder == "" {
		return env, fmt.Errorf(
			"workspaceID, workspaceUID or workspace folder not found: %s, %s, %s",
			env.id,
			env.uid,
			env.folder,
		)
	}

	return env, nil
}

func createFromEnv(ctx context.Context, baseClient client.Client) error {
	env, err := readWorkspaceEnv()
	if err != nil {
		return err
	}

	instance, err := platform.FindInstance(
		ctx,
		baseClient,
		platform.FindInstanceOptions{UID: env.uid},
	)
	if err != nil {
		return err
	}
	// Nothing left to do if we already have an instance
	if instance != nil {
		return nil
	}
	if !terminal.IsTerminalIn {
		return fmt.Errorf("unable to create new instance through CLI if stdin is not a terminal")
	}

	instance, err = form.CreateInstance(
		ctx,
		baseClient,
		env.id,
		env.uid,
		env.source,
		env.picture,
	)
	if err != nil {
		return err
	}

	_, err = createInstance(ctx, baseClient, instance)
	if err != nil {
		return err
	}

	return saveImportedWorkspaceConfig(instance, env.context, env.id)
}

func saveImportedWorkspaceConfig(
	instance *managementv1.DevsyWorkspaceInstance,
	workspaceContext, workspaceID string,
) error {
	// once we have the instance, update workspace and save config
	// TODO: Do we need a file lock?
	workspaceConfig, err := provider.LoadWorkspaceConfig(workspaceContext, workspaceID)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	workspaceConfig.Pro = &provider.ProMetadata{
		InstanceName: instance.GetName(),
		Project:      project.ProjectFromNamespace(instance.GetNamespace()),
		DisplayName:  instance.Spec.DisplayName,
	}

	err = provider.SaveWorkspaceConfig(workspaceConfig)
	if err != nil {
		return fmt.Errorf("save workspace config: %w", err)
	}

	return nil
}

func createInstance(
	ctx context.Context,
	client client.Client,
	instance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	managementClient, err := client.Management()
	if err != nil {
		return nil, err
	}

	updatedInstance, err := managementClient.Loft().ManagementV1().
		DevsyWorkspaceInstances(instance.GetNamespace()).
		Create(ctx, instance, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create workspace instance: %w", err)
	}

	return platform.WaitForInstance(ctx, client, updatedInstance)
}

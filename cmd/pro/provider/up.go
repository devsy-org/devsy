package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/remotecommand"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

// UpCmd holds the cmd flags:.
type UpCmd struct {
	*flags.GlobalFlags

	streams streams
}

type streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// NewUpCmd creates a new command.
func NewUpCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{
		GlobalFlags: globalFlags,
		streams: streams{
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		},
	}
	c := &cobra.Command{
		Hidden: true,
		Use:    "up",
		Short:  "Runs up on a workspace",
		Args:   cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return c
}

func (cmd *UpCmd) Run(ctx context.Context) error {
	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	info, err := platform.GetWorkspaceInfoFromEnv()
	if err != nil {
		return err
	}

	opts := platform.FindInstanceOptions{UID: info.UID, ProjectName: info.ProjectName}
	instance, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	} else if instance == nil {
		return fmt.Errorf(
			"workspace %s not found in project %s. Looks like it does not exist anymore and you can delete it",
			info.ID,
			info.ProjectName,
		)
	}

	// Log current workspace information. This is both useful to the user to understand the workspace configuration
	// and to us when we receive troubleshooting logs
	printInstanceInfo(instance)

	if instance.Spec.TemplateRef != nil && templateUpdateRequired(instance) {
		log.Info("Template update required")
		oldInstance := instance.DeepCopy()
		instance.Spec.TemplateRef.SyncOnce = true

		instance, err = platform.UpdateInstance(
			ctx,
			baseClient,
			oldInstance,
			instance,
			oldlog.Default,
		)
		if err != nil {
			return fmt.Errorf("update instance: %w", err)
		}
		log.Info("updated template")
	}

	return cmd.up(ctx, instance, baseClient)
}

func (cmd *UpCmd) up(
	ctx context.Context,
	workspace *managementv1.DevsyWorkspaceInstance,
	client client.Client,
) error {
	options := platform.OptionsFromEnv(storagev1.DevsyFlagsUp)
	if options != nil && os.Getenv(config.EnvDebug) == config.BoolTrue {
		options.Add("debug", config.BoolTrue)
	}

	conn, err := platform.DialInstance(client, workspace, "up", options, oldlog.Default)
	if err != nil {
		return err
	}

	_, err = remotecommand.ExecuteConn(
		ctx,
		conn,
		cmd.streams.Stdin,
		cmd.streams.Stdout,
		cmd.streams.Stderr,
		oldlog.Default.ErrorStreamOnly(),
	)
	if err != nil {
		return fmt.Errorf("error executing: %w", err)
	}

	return nil
}

func templateUpdateRequired(instance *managementv1.DevsyWorkspaceInstance) bool {
	var templateResolved, templateChangesAvailable bool
	for _, condition := range instance.Status.Conditions {
		if condition.Type == storagev1.InstanceTemplateResolved {
			templateResolved = condition.Status == corev1.ConditionTrue
			continue
		}

		if condition.Type == storagev1.InstanceTemplateSynced {
			templateChangesAvailable = condition.Status == corev1.ConditionFalse &&
				condition.Reason == "TemplateChangesAvailable"
			continue
		}
	}

	return !templateResolved || templateChangesAvailable
}

func printInstanceInfo(instance *managementv1.DevsyWorkspaceInstance) {
	workspaceConfig, _ := json.Marshal(struct {
		// Cluster    storagev1.WorkspaceTargetNamespace
		Template   *storagev1.TemplateRef
		Parameters string
	}{
		// Cluster:    cluster,
		// FIXME: Bring back runner ref
		Template:   instance.Spec.TemplateRef,
		Parameters: instance.Spec.Parameters,
	})
	log.Debugf("Starting pro workspace with configuration %s", string(workspaceConfig))
}

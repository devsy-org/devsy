package daemonclient

import (
	"context"
	"fmt"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (c *client) Delete(ctx context.Context, opt clientpkg.DeleteOptions) error {
	c.m.Lock()
	defer c.m.Unlock()

	baseClient, err := c.initPlatformClient(ctx)
	if err != nil {
		return err
	}

	workspace, err := platform.FindInstance(
		ctx,
		baseClient,
		platform.FindInstanceOptions{UID: c.workspace.UID},
	)
	if err != nil {
		return err
	}
	if workspace == nil {
		// delete the workspace folder
		return c.deleteWorkspaceFolder()
	}

	managementClient, err := baseClient.Management()
	if err != nil {
		return err
	}

	gracePeriod := parseGracePeriod(opt.GracePeriod)
	// kill the command after the grace period
	if gracePeriod != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *gracePeriod)
		defer cancel()
	}

	if err := c.deleteInstance(ctx, managementClient, workspace, opt.Force); err != nil {
		return err
	}

	// delete the workspace folder
	if err := c.deleteWorkspaceFolder(); err != nil {
		return err
	}

	// wait until the workspace is deleted
	log.Debugf("Waiting for workspace to get deleted")
	return c.waitForWorkspaceDeleted(ctx, managementClient, workspace, gracePeriod)
}

func parseGracePeriod(raw string) *time.Duration {
	if raw == "" {
		return nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		log.Warnf("Ignoring invalid grace period %q: %v", raw, err)
		return nil
	}

	return &duration
}

func (c *client) deleteWorkspaceFolder() error {
	return clientimplementation.DeleteWorkspaceFolder(
		clientimplementation.DeleteWorkspaceFolderParams{
			Context:              c.workspace.Context,
			WorkspaceID:          c.workspace.ID,
			SSHConfigPath:        c.workspace.SSHConfigPath,
			SSHConfigIncludePath: c.workspace.SSHConfigIncludePath,
		},
	)
}

func (c *client) deleteInstance(
	ctx context.Context,
	managementClient kube.Interface,
	workspace *managementv1.DevsyWorkspaceInstance,
	force bool,
) error {
	err := managementClient.Loft().
		ManagementV1().
		DevsyWorkspaceInstances(workspace.Namespace).
		Delete(ctx, workspace.Name, metav1.DeleteOptions{})
	if err != nil {
		if !force {
			return fmt.Errorf("delete workspace: %w", err)
		}

		if !kerrors.IsNotFound(err) {
			log.Errorf("Error deleting workspace: %v", err)
		}
	}

	return nil
}

func (c *client) waitForWorkspaceDeleted(
	ctx context.Context,
	managementClient kube.Interface,
	workspace *managementv1.DevsyWorkspaceInstance,
	gracePeriod *time.Duration,
) error {
	// calculate wait timeout
	waitTimeout := time.Minute
	if gracePeriod != nil {
		waitTimeout = *gracePeriod
	}

	err := wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		waitTimeout,
		false,
		func(ctx context.Context) (done bool, err error) {
			workspaceInstance, err := managementClient.Loft().
				ManagementV1().
				DevsyWorkspaceInstances(workspace.Namespace).
				Get(ctx, workspace.Name, metav1.GetOptions{})
			switch {
			case kerrors.IsNotFound(err):
				return true, nil
			case err != nil:
				return false, fmt.Errorf("error getting workspace: %w", err)
			case workspaceInstance.DeletionTimestamp == nil:
				// this can occur if the workspace is already deleted and was recreated
				return true, nil
			}

			log.Debugf("Workspace is not deleted yet, waiting again")
			return false, nil
		},
	)
	if err != nil {
		return fmt.Errorf("error waiting for workspace to get deleted: %w", err)
	}

	return nil
}

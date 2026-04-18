package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"

	managementv1 "github.com/skevetter/api/pkg/apis/management/v1"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/platform"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *client) Stop(ctx context.Context, opt clientpkg.StopOptions) error {
	c.m.Lock()
	defer c.m.Unlock()

	baseClient, err := c.initPlatformClient(ctx)
	if err != nil {
		return err
	}
	opts := platform.FindInstanceOptions{UID: c.workspace.UID}
	workspace, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	} else if workspace == nil {
		return fmt.Errorf("couldn't find workspace")
	}

	managementClient, err := baseClient.Management()
	if err != nil {
		return err
	}

	rawOptions, _ := json.Marshal(opt)
	retStop, err := managementClient.Loft().
		ManagementV1().
		DevsyWorkspaceInstances(workspace.Namespace).
		Stop(ctx, workspace.Name, &managementv1.DevsyWorkspaceInstanceStop{
			Spec: managementv1.DevsyWorkspaceInstanceStopSpec{
				Options: string(rawOptions),
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error stopping workspace: %w", err)
	} else if retStop.Status.TaskID == "" {
		return fmt.Errorf("no stop task id returned from server")
	}

	_, err = observeTask(ctx, managementClient, workspace, retStop.Status.TaskID, c.log)
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	return nil
}

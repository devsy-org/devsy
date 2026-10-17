package platform

import (
	"context"
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/pkg/platform/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateInstance creates a workspace instance in its namespace and waits until it is ready.
func CreateInstance(
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

	return WaitForInstance(ctx, client, updatedInstance)
}

// UpdateInstanceSyncTemplate is UpdateInstance with the template sync flag set,
// so the workspace template is kept up to date with configuration changes.
func UpdateInstanceSyncTemplate(
	ctx context.Context,
	client client.Client,
	oldInstance *managementv1.DevsyWorkspaceInstance,
	newInstance *managementv1.DevsyWorkspaceInstance,
) (*managementv1.DevsyWorkspaceInstance, error) {
	setTemplateSyncOnce(newInstance)

	return UpdateInstance(ctx, client, oldInstance, newInstance)
}

// setTemplateSyncOnce requests a one-time template sync. Instances without a template are left unchanged.
func setTemplateSyncOnce(instance *managementv1.DevsyWorkspaceInstance) {
	if instance.Spec.TemplateRef != nil {
		instance.Spec.TemplateRef.SyncOnce = true
	}
}

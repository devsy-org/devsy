package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *KubernetesDriver) createPersistentVolumeClaim(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
) error {
	pvc, err := k.buildPersistentVolumeClaim(id, options)
	if err != nil {
		return err
	}

	log.Debugf("Create Persistent Volume Claim %q", id)
	_, err = k.client.Client().
		CoreV1().
		PersistentVolumeClaims(k.namespace).
		Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pvc: %w", err)
	}

	return nil
}

func (k *KubernetesDriver) buildPersistentVolumeClaim(
	id string,
	options *driver.RunOptions,
) (*corev1.PersistentVolumeClaim, error) {
	containerInfo, err := k.getDevContainerInformation(id, options)
	if err != nil {
		return nil, err
	}

	size := k.options.DiskSize
	quantity, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, fmt.Errorf("parse persistent volume size %q: %w", size, err)
	}

	var storageClassName *string
	if k.options.StorageClass != "" {
		storageClassName = &k.options.StorageClass
	}
	accessMode := resolveAccessMode(k.options.PvcAccessMode)

	labels := map[string]string{}
	maps.Copy(labels, ExtraDevsyLabels)
	maps.Copy(labels, pkgconfig.K8sVolumeLabels(options.UID, pkgconfig.VolumeRoleWorkspace))

	annotations := k.buildPvcAnnotations(containerInfo)

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        id,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: accessMode,
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: quantity,
				},
			},
			StorageClassName: storageClassName,
		},
	}, nil
}

func resolveAccessMode(mode string) []corev1.PersistentVolumeAccessMode {
	switch mode {
	case "ROX":
		return []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}
	case "RWX":
		return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	case "RWOP":
		return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}
	default:
		return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}
}

func (k *KubernetesDriver) buildPvcAnnotations(containerInfo string) map[string]string {
	annotations := map[string]string{}
	annotations[DevsyInfoAnnotation] = containerInfo
	extraAnnotations, err := parseLabels(k.options.PvcAnnotations)
	if err != nil {
		log.Errorf("Failed to parse annotations from PVC_ANNOTATIONS option: %v", err)
	}
	maps.Copy(annotations, extraAnnotations)

	return annotations
}

func (k *KubernetesDriver) getDevContainerInformation(
	id string,
	options *driver.RunOptions,
) (string, error) {
	containerInfo, err := json.Marshal(&DevContainerInfo{
		WorkspaceID: id,
		Options:     options,
	})
	if err != nil {
		return "", err
	}

	return string(containerInfo), nil
}

func (k *KubernetesDriver) resolveRunOptions(
	saved *DevContainerInfo,
	options *driver.RunOptions,
) (*driver.RunOptions, error) {
	if options == nil && saved != nil {
		options = saved.Options
	}
	if options == nil {
		return nil, fmt.Errorf("no options provided and no saved run configuration available")
	}
	resolved, err := k.refreshStoredLifecycle(options)
	if err != nil {
		return nil, err
	}
	if err := validateImageMainRecipe(resolved); err != nil {
		return nil, err
	}
	if err := k.preflightTemplate(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// A single resource-version-guarded update publishes intent before pod/config changes.
func (k *KubernetesDriver) persistRunOptions(
	ctx context.Context,
	pvc *corev1.PersistentVolumeClaim,
	saved *DevContainerInfo,
	options *driver.RunOptions,
) error {
	if err := verifyVolumeOwner(pvc, saved, options.UID); err != nil {
		return err
	}
	if reflect.DeepEqual(saved.Options, options) {
		return nil
	}
	next := *saved
	next.Options = options
	raw, err := json.Marshal(&next)
	if err != nil {
		return err
	}
	updated := pvc.DeepCopy()
	updated.Annotations[DevsyInfoAnnotation] = string(raw)
	_, err = k.client.Client().CoreV1().PersistentVolumeClaims(k.namespace).
		Update(ctx, updated, metav1.UpdateOptions{})
	if kerrors.IsForbidden(err) {
		return fmt.Errorf(
			"persist workspace run configuration: Kubernetes permission update on "+
				"persistentvolumeclaims in namespace %q is required for changed run configuration: %w",
			k.namespace, err,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"persist workspace run configuration; no pod/config changes were made: %w",
			err,
		)
	}
	return nil
}

func verifyVolumeOwner(
	pvc *corev1.PersistentVolumeClaim,
	info *DevContainerInfo,
	workspaceUID string,
) error {
	if uid := pvc.Labels[DevsyWorkspaceUIDLabel]; uid != "" && uid != workspaceUID {
		return fmt.Errorf("workspace volume %q belongs to a different workspace", pvc.Name)
	}
	if info.Options != nil && info.Options.UID != "" && info.Options.UID != workspaceUID {
		return fmt.Errorf("workspace volume %q has run options for a different workspace", pvc.Name)
	}
	return nil
}

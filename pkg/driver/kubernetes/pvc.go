package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
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

	log.Infof("Create Persistent Volume Claim %q", id)
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

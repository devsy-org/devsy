package kubernetes

import (
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	corev1 "k8s.io/api/core/v1"
)

func (k *KubernetesDriver) getInitContainers(
	options *driver.RunOptions,
	pod *corev1.Pod,
	initialize bool,
) []corev1.Container {
	if !initialize {
		// don't build init container and clean up existing one if defined
		return filterOutInitContainer(pod.Spec.InitContainers)
	}

	volumeMounts, commands := buildVolumeCopyCommands(options)

	retContainers, existingInitContainer := splitInitContainers(pod.Spec.InitContainers)

	// check if there is at least one mount
	if len(volumeMounts) == 0 {
		return retContainers
	}

	securityContext := &corev1.SecurityContext{
		RunAsUser:    &[]int64{0}[0],
		RunAsGroup:   &[]int64{0}[0],
		RunAsNonRoot: &[]bool{false}[0],
	}
	if k.options.StrictSecurity == pkgconfig.BoolTrue {
		securityContext = nil
	}

	resources := corev1.ResourceRequirements{}
	if existingInitContainer != nil {
		resources = existingInitContainer.Resources
	}

	initContainer := corev1.Container{
		Name:            InitContainerName,
		Image:           options.Image,
		Command:         []string{"sh"},
		Args:            []string{"-c", strings.Join(commands, "\n") + "\n"},
		Resources:       resources,
		VolumeMounts:    volumeMounts,
		SecurityContext: securityContext,
	}

	mergeContainer(&initContainer, existingInitContainer)

	retContainers = append(retContainers, initContainer)
	return retContainers
}

func filterOutInitContainer(containers []corev1.Container) []corev1.Container {
	retContainers := []corev1.Container{}
	for _, container := range containers {
		if container.Name == InitContainerName {
			continue
		}
		retContainers = append(retContainers, container)
	}

	return retContainers
}

func buildVolumeCopyCommands(
	options *driver.RunOptions,
) ([]corev1.VolumeMount, []string) {
	commands := []string{}
	volumeMounts := []corev1.VolumeMount{}
	for idx, mount := range options.Mounts {
		if mount.Type != "volume" {
			continue
		}

		volumeMount := getVolumeMount(idx+1, mount)
		copyFrom := volumeMount.MountPath
		volumeMount.MountPath = "/" + volumeMount.SubPath
		volumeMounts = append(volumeMounts, volumeMount)
		commands = append(
			commands,
			fmt.Sprintf(
				`cp -a %s/. %s/ || true`,
				strings.TrimRight(copyFrom, "/"),
				strings.TrimRight(volumeMount.MountPath, "/"),
			),
		)
	}

	return volumeMounts, commands
}

func splitInitContainers(containers []corev1.Container) ([]corev1.Container, *corev1.Container) {
	retContainers := []corev1.Container{}
	var existingInitContainer *corev1.Container
	for i, container := range containers {
		if container.Name == InitContainerName {
			existingInitContainer = &containers[i]
		} else {
			retContainers = append(retContainers, container)
		}
	}

	return retContainers, existingInitContainer
}

func mergeContainer(dst, src *corev1.Container) {
	if src == nil {
		return
	}

	dst.Env = append(src.Env, dst.Env...)
	dst.EnvFrom = src.EnvFrom
	dst.Ports = src.Ports
	dst.VolumeMounts = append(
		src.VolumeMounts,
		dst.VolumeMounts...)
	dst.ImagePullPolicy = src.ImagePullPolicy

	if dst.SecurityContext == nil && src.SecurityContext != nil {
		dst.SecurityContext = src.SecurityContext
	}
}

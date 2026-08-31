package kubernetes

import (
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/driver"
	corev1 "k8s.io/api/core/v1"
)

func (k *KubernetesDriver) getInitContainers(
	options *driver.RunOptions,
	pod *corev1.Pod,
	initialize bool,
) ([]corev1.Container, error) {
	if !initialize {
		return filterOutInitContainer(pod.Spec.InitContainers), nil
	}

	volumeMounts, commands := buildVolumeCopyCommands(options)

	retContainers, existingInitContainer := splitInitContainers(pod.Spec.InitContainers)
	if len(volumeMounts) == 0 {
		return retContainers, nil
	}

	securityContext, err := (securityContextOptions{
		StrictSecurity:       k.options.StrictSecurity,
		AgentSecurityContext: k.options.AgentSecurityContext,
	}).resolve()
	if err != nil {
		return nil, err
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
	return retContainers, nil
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

func overrideIfSet[T any](dst **T, src *T) {
	if src != nil {
		*dst = src
	}
}

func mergeSecurityContext(dst, src *corev1.SecurityContext) *corev1.SecurityContext {
	if src == nil {
		return dst
	}
	merged := corev1.SecurityContext{}
	if dst != nil {
		merged = *dst
	}
	if src.RunAsUser == nil &&
		src.RunAsNonRoot != nil && *src.RunAsNonRoot &&
		merged.RunAsUser != nil && *merged.RunAsUser == 0 {
		// A template that sets runAsNonRoot=true without runAsUser must
		// remove the generated container's implicit root UID.
		merged.RunAsUser = nil
	}
	overrideIfSet(&merged.Capabilities, src.Capabilities)
	overrideIfSet(&merged.Privileged, src.Privileged)
	overrideIfSet(&merged.SELinuxOptions, src.SELinuxOptions)
	overrideIfSet(&merged.WindowsOptions, src.WindowsOptions)
	overrideIfSet(&merged.RunAsUser, src.RunAsUser)
	overrideIfSet(&merged.RunAsGroup, src.RunAsGroup)
	overrideIfSet(&merged.RunAsNonRoot, src.RunAsNonRoot)
	overrideIfSet(&merged.ReadOnlyRootFilesystem, src.ReadOnlyRootFilesystem)
	overrideIfSet(&merged.AllowPrivilegeEscalation, src.AllowPrivilegeEscalation)
	overrideIfSet(&merged.ProcMount, src.ProcMount)
	overrideIfSet(&merged.SeccompProfile, src.SeccompProfile)
	overrideIfSet(&merged.AppArmorProfile, src.AppArmorProfile)
	return &merged
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

	dst.SecurityContext = mergeSecurityContext(dst.SecurityContext, src.SecurityContext)
}

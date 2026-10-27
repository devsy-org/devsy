package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/devsy-org/devsy/pkg/driver"
	corev1 "k8s.io/api/core/v1"
)

type podTopology struct {
	dockerless bool
	imageMain  bool
}

func decodePodTopology(pod *corev1.Pod) (podTopology, bool, error) {
	rawDockerless, hasDockerless := pod.Annotations[DevsyDockerlessAnnotation]
	rawImageMain, hasImageMain := pod.Annotations[DevsyImageEntrypointAnnotation]
	if !hasDockerless && !hasImageMain {
		return podTopology{}, false, nil
	}
	dockerless, err := strconv.ParseBool(rawDockerless)
	if err != nil {
		return podTopology{}, false, fmt.Errorf(
			"pod %q has invalid Dockerless topology metadata",
			pod.Name,
		)
	}
	imageMain, err := strconv.ParseBool(rawImageMain)
	if err != nil {
		return podTopology{}, false, fmt.Errorf(
			"pod %q has invalid image-main topology metadata",
			pod.Name,
		)
	}
	return podTopology{dockerless: dockerless, imageMain: imageMain}, true, nil
}

func podLaunchDiffers(pod *corev1.Pod, options *driver.RunOptions) bool {
	topology, known, err := decodePodTopology(pod)
	if err == nil && known &&
		(topology.dockerless != options.Dockerless || topology.imageMain != options.ImageEntrypoint) {
		return true
	}
	return podCommandDiffers(pod, options)
}

func podCommandDiffers(pod *corev1.Pod, options *driver.RunOptions) bool {
	var command []string
	if options.Entrypoint != "" {
		command = []string{options.Entrypoint}
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == DevContainerName {
			return container.Image != options.Image ||
				!slices.Equal(
					container.Command,
					command,
				) || !slices.Equal(container.Args, options.Cmd)
		}
	}
	return true
}

func observedPodTopology(pod *corev1.Pod, options *driver.RunOptions) (bool, bool, error) {
	if pod == nil {
		return options.Dockerless, options.ImageEntrypoint, validateImageMainRecipe(options)
	}
	if err := verifyOwnedPod(pod, pod.Name, options.UID); err != nil {
		return false, false, err
	}
	topology, known, err := decodePodTopology(pod)
	if err != nil {
		return false, false, err
	}
	if known {
		return topology.dockerless, topology.imageMain, nil
	}
	if podCommandDiffers(pod, options) {
		return false, false, fmt.Errorf(
			"legacy pod %q does not match the saved launch recipe; run devsy workspace up --recreate",
			pod.Name,
		)
	}
	return options.Dockerless, options.ImageEntrypoint, validateImageMainRecipe(options)
}

func validateImageMainRecipe(options *driver.RunOptions) error {
	// The legacy producer framed the script with three shell arguments, then
	// appended the image main. Missing facts on that specific recipe are ambiguous.
	if !options.Dockerless && !options.ImageEntrypoint && options.Entrypoint == "/bin/sh" &&
		len(options.Cmd) > 3 && options.Cmd[0] == "-c" && options.Cmd[2] == "-" {
		return fmt.Errorf(
			"saved shell recipe carries an external command without its image-main fact; run devsy workspace up --recreate",
		)
	}
	return nil
}

func (k *KubernetesDriver) verifyDesiredPod(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
) error {
	pvc, info, err := k.getDevContainerPvc(ctx, id)
	if err != nil {
		return err
	}
	if pvc == nil {
		return fmt.Errorf("workspace volume %q disappeared during startup", id)
	}
	if err := verifyVolumeOwner(pvc, info, options.UID); err != nil {
		return err
	}
	if !reflect.DeepEqual(info.Options, options) {
		return fmt.Errorf(
			"workspace %q desired run configuration changed during startup; retry the operation",
			id,
		)
	}
	pod, err := k.getPod(ctx, id)
	if err != nil {
		return err
	}
	if pod == nil {
		return errPodChanged
	}
	return verifyDesiredTopology(pod, options)
}

func verifyDesiredTopology(pod *corev1.Pod, options *driver.RunOptions) error {
	dockerless, imageMain, err := observedPodTopology(pod, options)
	if err != nil {
		return err
	}
	if dockerless != options.Dockerless || imageMain != options.ImageEntrypoint ||
		podCommandDiffers(pod, options) {
		return fmt.Errorf(
			"pod %q no longer matches the desired launch topology; run devsy workspace up --recreate",
			pod.Name,
		)
	}
	return nil
}

func (k *KubernetesDriver) verifyExistingPodOwner(
	ctx context.Context,
	id, workspaceUID string,
) error {
	pod, err := k.getPod(ctx, id)
	if err != nil {
		return err
	}
	if pod == nil {
		return nil
	}
	if err := verifyOwnedPod(pod, id, workspaceUID); err != nil {
		return err
	}
	_, _, err = decodePodTopology(pod)
	return err
}

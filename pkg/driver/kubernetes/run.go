package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	DevContainerName  = pkgconfig.BinaryName
	InitContainerName = pkgconfig.BinaryName + "-init"
)

const (
	DevsyCreatedLabel      = pkgconfig.BinaryName + ".sh/created"
	DevsyWorkspaceLabel    = pkgconfig.BinaryName + ".sh/workspace"
	DevsyWorkspaceUIDLabel = pkgconfig.BinaryName + ".sh/workspace-uid"

	DevsyInfoAnnotation                    = pkgconfig.BinaryName + ".sh/info"
	DevsyLastAppliedAnnotation             = pkgconfig.BinaryName + ".sh/last-applied-configuration"
	ClusterAutoscalerSaveToEvictAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict"
)

var ExtraDevsyLabels = map[string]string{
	DevsyCreatedLabel: "true",
}

type DevContainerInfo struct {
	WorkspaceID string
	Options     *driver.RunOptions
}

func (k *KubernetesDriver) RunDevContainer(
	ctx context.Context,
	workspaceId string,
	options *driver.RunOptions,
) error {
	log.Debugf("Running devcontainer for workspace %q", workspaceId)
	workspaceId = getID(workspaceId)

	// namespace
	if k.namespace != "" && k.options.CreateNamespace == pkgconfig.BoolTrue {
		err := k.createNamespace(ctx)
		if err != nil {
			return err
		}
	}

	// check if persistent volume claim already exists
	initialize := false
	pvc, containerInfo, err := k.getDevContainerPvc(ctx, workspaceId)
	if err != nil {
		return err
	} else if pvc == nil {
		if options == nil {
			return fmt.Errorf(
				"no options provided and no persistent volume claim found for workspace %q",
				workspaceId,
			)
		}

		// create persistent volume claim
		err = k.createPersistentVolumeClaim(ctx, workspaceId, options)
		if err != nil {
			return err
		}

		initialize = true
	}

	// reuse driver.RunOptions from existing workspace if none provided
	if options == nil && containerInfo != nil && containerInfo.Options != nil {
		options = containerInfo.Options
	}

	// create dev container
	err = k.runContainer(ctx, workspaceId, options, initialize)
	if err != nil {
		return err
	}

	return nil
}

func (k *KubernetesDriver) runContainer(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
	initialize bool,
) (err error) {
	// get workspace mount
	mount := options.WorkspaceMount
	if mount == nil {
		return fmt.Errorf(
			"workspace mount is suppressed; cannot run in Kubernetes without a workspace mount",
		)
	}
	if mount.Target == "" {
		return fmt.Errorf("workspace mount target is empty")
	}
	if k.options.WorkspaceVolumeMount != "" {
		// Ensure workspace volume mount option is parent or same dir as workspace mount
		rel, err := filepath.Rel(k.options.WorkspaceVolumeMount, mount.Target)
		if err != nil {
			log.Warnf("Relative filepath: %v", err)
		} else if strings.HasPrefix(rel, "..") {
			log.Warnf(
				"Workspace volume mount needs to be the same as the workspace mount or a parent, skipping option. "+
					"WorkspaceVolumeMount: %s, MountTarget: %s",
				k.options.WorkspaceVolumeMount,
				mount.Target,
			)
		} else {
			mount.Target = k.options.WorkspaceVolumeMount
			log.Debugf("Using workspace volume mount: %s", k.options.WorkspaceVolumeMount)
		}
	}

	// read pod template
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	if len(k.options.PodManifestTemplate) > 0 {
		log.Debugf("trying to get pod template manifest from %s", k.options.PodManifestTemplate)
		pod, err = getPodTemplate(k.options.PodManifestTemplate)
		if err != nil {
			return err
		}
	}

	// get init containers
	initContainers, err := k.getInitContainers(options, pod, initialize)
	if err != nil {
		return fmt.Errorf("build init container: %w", err)
	}

	// loop over volume mounts
	volumeMounts := []corev1.VolumeMount{getVolumeMount(0, mount)}
	for idx, mount := range options.Mounts {
		volumeMount := getVolumeMount(idx+1, mount)
		if mount.Type == "bind" || mount.Type == "volume" {
			volumeMounts = append(volumeMounts, volumeMount)
		} else {
			log.Warnf(
				"Unsupported mount type %q in mount %q, will skip",
				mount.Type,
				mount.String(),
			)
		}
	}

	// capabilities
	var capabilities *corev1.Capabilities
	if len(options.CapAdd) > 0 {
		capabilities = &corev1.Capabilities{}
		for _, cap := range options.CapAdd {
			capabilities.Add = append(capabilities.Add, corev1.Capability(cap))
		}
	}

	// env vars
	envVars := []corev1.EnvVar{}
	daemonConfig := ""
	for k, v := range options.Env {
		// filter out daemon config, that's going to be mounted through a secret
		if k == pkgconfig.EnvWorkspaceDaemonConfig {
			daemonConfig = v
			continue
		}
		envVars = append(envVars, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	// service account
	serviceAccount := ""
	if k.options.ServiceAccount != "" {
		serviceAccount = k.options.ServiceAccount

		// create service account
		err = k.createServiceAccount(ctx, id, serviceAccount)
		if err != nil {
			return fmt.Errorf("create service account: %w", err)
		}
	}

	// labels
	labels, err := getLabels(pod, k.options.Labels)
	if err != nil {
		return err
	}
	labels[DevsyWorkspaceUIDLabel] = options.UID

	// node selector
	nodeSelector, err := getNodeSelector(pod, k.options.NodeSelector)
	if err != nil {
		return err
	}

	// parse resources
	resources := corev1.ResourceRequirements{}
	if len(pod.Spec.Containers) > 0 {
		resources = pod.Spec.Containers[0].Resources
	}
	if k.options.Resources != "" {
		resources = parseResources(k.options.Resources)
	}

	// ensure daemon config secret
	daemonConfigSecretName := ""
	if daemonConfig != "" {
		daemonConfigSecretName = getDaemonSecretName(id)
		err = k.EnsureDaemonConfigSecret(ctx, daemonConfigSecretName, daemonConfig)
		if err != nil {
			return err
		}
	}

	// ensure pull secrets
	pullSecretsCreated := false
	if k.options.KubernetesPullSecretsEnabled == pkgconfig.BoolTrue &&
		k.agentConfig.InjectDockerCredentials == pkgconfig.BoolTrue {
		pullSecretsCreated, err = k.EnsurePullSecret(ctx, getPullSecretsName(id), options.Image)
		if err != nil {
			return err
		}
	}

	// create the pod manifest
	pod.Name = id
	pod.Labels = labels

	pod.Spec.ServiceAccountName = serviceAccount
	pod.Spec.NodeSelector = nodeSelector
	pod.Spec.InitContainers = initContainers
	pod.Spec.Containers = getContainers(
		pod,
		options.Image,
		options.Entrypoint,
		options.Cmd,
		envVars,
		volumeMounts,
		capabilities,
		resources,
		options.Privileged,
		k.options.StrictSecurity,
		daemonConfigSecretName,
	)
	pod.Spec.Volumes = getVolumes(pod, id, daemonConfigSecretName)
	// avoids a problem where attaching volumes with large repositories would cause an extremely long pod startup time
	// because changing the ownership of all files takes longer than the kubelet expects it to
	if pod.Spec.SecurityContext == nil {
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{
			FSGroupChangePolicy: ptr.To(corev1.FSGroupChangeOnRootMismatch),
		}
	}
	if k.options.KubernetesPullSecretsEnabled == pkgconfig.BoolTrue && pullSecretsCreated {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: getPullSecretsName(id)}}
	}
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever
	// try to get existing pod
	existingPod, err := k.getPod(ctx, id)
	if err != nil {
		return fmt.Errorf("get pod: %s: %w", id, err)
	}

	if existingPod != nil {
		existingOptions := &provider2.ProviderKubernetesDriverConfig{}
		err := json.Unmarshal(
			[]byte(existingPod.GetAnnotations()[DevsyLastAppliedAnnotation]),
			existingOptions,
		)
		if err != nil {
			log.Errorf("Error unmarshalling existing provider options, continuing...: %s", err)
		}

		// Nothing changed, can safely return
		if optionsEqual(existingOptions, k.options) {
			log.Infof(
				"Pod %q already exists and nothing changed, skipping update",
				existingPod.Name,
			)
			return nil
		}

		// Stop the current pod
		log.Debug("Provider options changed")
		err = k.waitPodDeleted(ctx, id)
		if err != nil {
			return fmt.Errorf("stop devcontainer: %s: %w", id, err)
		}
	}

	err = k.runPod(ctx, id, pod)
	if err != nil {
		return err
	}

	return nil
}

func (k *KubernetesDriver) runPod(ctx context.Context, id string, pod *corev1.Pod) error {
	var err error

	// set configuration before creating the pod
	lastAppliedConfigRaw, err := json.Marshal(k.options)
	if err != nil {
		return fmt.Errorf("marshal last applied config: %w", err)
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[DevsyLastAppliedAnnotation] = string(lastAppliedConfigRaw)
	pod.Annotations[ClusterAutoscalerSaveToEvictAnnotation] = "false"

	// marshal the pod
	podRaw, err := json.Marshal(pod)
	if err != nil {
		return err
	}

	log.Debugf("Create pod with: %s", string(podRaw))

	// create the pod
	log.Infof("Create Pod %q", id)
	_, err = k.client.Client().CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod: %w", err)
	}

	// wait for pod running
	log.Infof("Waiting for DevContainer Pod %q to come up...", id)
	_, err = k.waitPodRunning(ctx, id)
	if err != nil {
		return err
	}

	return nil
}

func getContainers(
	pod *corev1.Pod,
	imageName,
	entrypoint string,
	args []string,
	envVars []corev1.EnvVar,
	volumeMounts []corev1.VolumeMount,
	capabilities *corev1.Capabilities,
	resources corev1.ResourceRequirements,
	privileged *bool,
	strictSecurity string,
	daemonConfigSecretName string,
) []corev1.Container {
	if daemonConfigSecretName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      DevContainerName + "-daemon-config",
			MountPath: "/var/run/secrets/" + DevContainerName,
		})
	}
	devsyContainer := corev1.Container{
		Name:         DevContainerName,
		Image:        imageName,
		Command:      []string{entrypoint},
		Args:         args,
		Env:          envVars,
		Resources:    resources,
		VolumeMounts: volumeMounts,
		SecurityContext: &corev1.SecurityContext{
			Capabilities: capabilities,
			Privileged:   privileged,
			RunAsUser:    &[]int64{0}[0],
			RunAsGroup:   &[]int64{0}[0],
			RunAsNonRoot: &[]bool{false}[0],
		},
	}

	if strictSecurity == pkgconfig.BoolTrue {
		devsyContainer.SecurityContext = nil
	}

	// merge with existing container if it exists
	var existingDevsyContainer *corev1.Container
	retContainers := []corev1.Container{}
	if pod != nil {
		for i, container := range pod.Spec.Containers {
			if container.Name == DevContainerName {
				existingDevsyContainer = &pod.Spec.Containers[i]
			} else {
				retContainers = append(retContainers, container)
			}
		}
	}

	if existingDevsyContainer != nil {
		devsyContainer.Env = append(existingDevsyContainer.Env, devsyContainer.Env...)
		devsyContainer.EnvFrom = existingDevsyContainer.EnvFrom
		devsyContainer.Ports = existingDevsyContainer.Ports
		devsyContainer.VolumeMounts = append(
			existingDevsyContainer.VolumeMounts,
			devsyContainer.VolumeMounts...)
		devsyContainer.ImagePullPolicy = existingDevsyContainer.ImagePullPolicy

		if devsyContainer.SecurityContext == nil &&
			existingDevsyContainer.SecurityContext != nil {
			devsyContainer.SecurityContext = existingDevsyContainer.SecurityContext
		}
	}
	retContainers = append(retContainers, devsyContainer)

	return retContainers
}

func getVolumes(pod *corev1.Pod, id string, daemonConfigSecretName string) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: DevContainerName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: id,
				},
			},
		},
	}

	if daemonConfigSecretName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: DevContainerName + "-daemon-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: daemonConfigSecretName,
				},
			},
		})
	}

	if pod.Spec.Volumes != nil {
		volumes = append(volumes, pod.Spec.Volumes...)
	}

	return volumes
}

func getVolumeMount(idx int, mount *config.Mount) corev1.VolumeMount {
	subPath := strconv.Itoa(idx)
	if mount.Type == "volume" && mount.Source != "" {
		subPath = strings.TrimPrefix(mount.Source, "/")
	}

	return corev1.VolumeMount{
		Name:      DevContainerName,
		MountPath: mount.Target,
		SubPath:   fmt.Sprintf(DevContainerName+"/%s", subPath),
	}
}

func getLabels(pod *corev1.Pod, rawLabels string) (map[string]string, error) {
	labels := map[string]string{}
	if pod.Labels != nil {
		maps.Copy(labels, pod.Labels)
	}
	if rawLabels != "" {
		extraLabels, err := parseLabels(rawLabels)
		if err != nil {
			return nil, fmt.Errorf("parse labels: %w", err)
		}
		maps.Copy(labels, extraLabels)
	}
	// make sure we don't overwrite the devsy labels
	maps.Copy(labels, ExtraDevsyLabels)

	return labels, nil
}

func getNodeSelector(pod *corev1.Pod, rawNodeSelector string) (map[string]string, error) {
	nodeSelector := map[string]string{}
	if pod.Spec.NodeSelector != nil {
		maps.Copy(nodeSelector, pod.Spec.NodeSelector)
	}

	if rawNodeSelector != "" {
		selector, err := parseLabels(rawNodeSelector)
		if err != nil {
			return nil, fmt.Errorf("parsing node selector: %w", err)
		}
		maps.Copy(nodeSelector, selector)
	}

	return nodeSelector, nil
}

func (k *KubernetesDriver) StartDevContainer(ctx context.Context, workspaceId string) error {
	log.Debugf("Starting devcontainer for workspace %q", workspaceId)
	defer log.Debugf("Done starting devcontainer for workspace %q", workspaceId)

	workspaceId = getID(workspaceId)
	_, containerInfo, err := k.getDevContainerPvc(ctx, workspaceId)
	if err != nil {
		return err
	} else if containerInfo == nil {
		return fmt.Errorf("persistent volume %q not found", workspaceId)
	}

	return k.runContainer(
		ctx,
		workspaceId,
		containerInfo.Options,
		false,
	)
}

func getID(workspaceID string) string {
	return DevContainerName + "-" + workspaceID
}

func getPullSecretsName(workspaceID string) string {
	return fmt.Sprintf(DevContainerName+"-pull-secret-%s", workspaceID)
}

func getDaemonSecretName(workspaceID string) string {
	return fmt.Sprintf(DevContainerName+"-daemon-secret-%s", workspaceID)
}

func optionsEqual(a, b *provider2.ProviderKubernetesDriverConfig) bool {
	// copy a and b and the compare them without the context, config, namespace and podTimeout
	aCopy := *a
	aCopy.KubernetesContext = ""
	aCopy.KubernetesConfig = ""
	aCopy.KubernetesNamespace = ""
	aCopy.PodTimeout = ""

	bCopy := *b
	bCopy.KubernetesContext = ""
	bCopy.KubernetesConfig = ""
	bCopy.KubernetesNamespace = ""
	bCopy.PodTimeout = ""
	return aCopy == bCopy
}

func (k *KubernetesDriver) createNamespace(ctx context.Context) error {
	_, err := k.client.Client().CoreV1().Namespaces().Get(ctx, k.namespace, metav1.GetOptions{})
	if kerrors.IsNotFound(err) || kerrors.IsForbidden(err) {
		log.Infof("Create namespace %q", k.namespace)
		_, err := k.client.Client().CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: k.namespace,
			},
		}, metav1.CreateOptions{})
		if err != nil && !kerrors.IsAlreadyExists(err) && !kerrors.IsForbidden(err) {
			return fmt.Errorf("create namespace: %w", err)
		}
	}

	return nil
}

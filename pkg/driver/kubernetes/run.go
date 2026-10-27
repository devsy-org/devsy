package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	DevsyCreatedLabel              = pkgconfig.K8sCreatedLabel
	DevsyWorkspaceLabel            = pkgconfig.K8sWorkspaceLabel
	DevsyWorkspaceUIDLabel         = pkgconfig.K8sWorkspaceUIDLabel
	DevsyInfoAnnotation            = pkgconfig.K8sInfoAnnotation
	DevsyLastAppliedAnnotation     = pkgconfig.K8sLastAppliedAnnotation
	DevsyDockerlessAnnotation      = DevsyInfoAnnotation + "-dockerless"
	DevsyImageEntrypointAnnotation = DevsyInfoAnnotation + "-image-entrypoint"
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

	initialize, options, err := k.ensureDevContainerPvc(ctx, workspaceId, options)
	if err != nil {
		return err
	}

	if err := k.runContainer(ctx, workspaceId, options, initialize); err != nil {
		return err
	}
	return k.verifyDesiredPod(ctx, workspaceId, options)
}

func (k *KubernetesDriver) ValidateRunImageDevContainer(
	params *driver.RunImageDevContainerParams,
) error {
	_, err := k.resolveRunOptions(nil, params.Options)
	return err
}

// preflightTemplate rejects an unusable pod template before any cluster object,
// including the workspace volume, is created.
func (k *KubernetesDriver) preflightTemplate(options *driver.RunOptions) error {
	if options == nil {
		return nil
	}
	pod, err := loadPodTemplate(k.options.PodManifestTemplate)
	if err != nil {
		return err
	}
	timeout, err := lifecycleTimeout(options)
	if err != nil {
		return fmt.Errorf("read lifecycle policy: %w", err)
	}
	return validateNoExtraContainers(pod, timeout)
}

func (k *KubernetesDriver) ensureNamespace(ctx context.Context) error {
	if k.namespace == "" || k.options.CreateNamespace != pkgconfig.BoolTrue {
		return nil
	}

	return k.createNamespace(ctx)
}

func (k *KubernetesDriver) ensureDevContainerPvc(
	ctx context.Context,
	workspaceId string,
	options *driver.RunOptions,
) (bool, *driver.RunOptions, error) {
	pvc, containerInfo, err := k.getDevContainerPvc(ctx, workspaceId)
	if err != nil {
		return false, nil, err
	}
	options, err = k.resolveRunOptions(containerInfo, options)
	if err != nil {
		return false, nil, err
	}
	if err := k.verifyExistingPodOwner(ctx, workspaceId, options.UID); err != nil {
		return false, nil, err
	}
	if pvc == nil {
		if err := k.ensureNamespace(ctx); err != nil {
			return false, nil, err
		}

		if err := k.createPersistentVolumeClaim(ctx, workspaceId, options); err != nil {
			return false, nil, err
		}

		return true, options, nil
	}

	if err := k.persistRunOptions(ctx, pvc, containerInfo, options); err != nil {
		return false, nil, err
	}

	return false, options, nil
}

func (k *KubernetesDriver) runContainer(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
	initialize bool,
) error {
	if options == nil {
		return fmt.Errorf("no run options provided for workspace %q", id)
	}

	pod, err := k.buildPod(ctx, id, options, initialize)
	if err != nil {
		return err
	}

	timeout, err := lifecycleTimeout(options)
	if err != nil {
		return fmt.Errorf("read lifecycle policy: %w", err)
	}
	if err := validatePodTopology(pod, timeout); err != nil {
		return err
	}

	skip, err := k.reconcileExistingPod(ctx, id, options)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	return k.runPod(ctx, id, pod, timeout)
}

func (k *KubernetesDriver) buildPod(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
	initialize bool,
) (*corev1.Pod, error) {
	mount, err := k.resolveWorkspaceMount(options)
	if err != nil {
		return nil, err
	}

	pod, err := loadPodTemplate(k.options.PodManifestTemplate)
	if err != nil {
		return nil, err
	}
	// Reject before any cluster object is created for this run.
	timeout, err := lifecycleTimeout(options)
	if err != nil {
		return nil, fmt.Errorf("read lifecycle policy: %w", err)
	}
	if err := validateNoExtraContainers(pod, timeout); err != nil {
		return nil, err
	}

	initContainers, err := k.getInitContainers(options, pod, initialize)
	if err != nil {
		return nil, err
	}
	containerInputs := k.buildPodContainerInputs(options, mount)
	serviceAccount, err := k.ensureServiceAccount(ctx, id)
	if err != nil {
		return nil, err
	}

	meta, err := k.buildPodMetadata(pod, options)
	if err != nil {
		return nil, err
	}

	daemonConfigSecretName, err := k.ensureDaemonConfig(ctx, id, containerInputs.daemonConfig)
	if err != nil {
		return nil, err
	}

	pullSecretsCreated, err := k.ensurePullSecrets(ctx, id, options.Image)
	if err != nil {
		return nil, err
	}

	if err := k.assemblePodSpec(pod, id, &podSpecInputs{
		options:                options,
		meta:                   meta,
		initContainers:         initContainers,
		volumeMounts:           containerInputs.volumeMounts,
		tmpfsVolumes:           containerInputs.tmpfsVolumes,
		capabilities:           containerInputs.capabilities,
		envVars:                containerInputs.envVars,
		serviceAccount:         serviceAccount,
		daemonConfigSecretName: daemonConfigSecretName,
		pullSecretsCreated:     pullSecretsCreated,
	}); err != nil {
		return nil, err
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[DevsyDockerlessAnnotation] = strconv.FormatBool(options.Dockerless)
	pod.Annotations[DevsyImageEntrypointAnnotation] = strconv.FormatBool(options.ImageEntrypoint)

	return pod, nil
}

type podSpecInputs struct {
	options                *driver.RunOptions
	meta                   *podMetadata
	initContainers         []corev1.Container
	volumeMounts           []corev1.VolumeMount
	tmpfsVolumes           []corev1.Volume
	capabilities           *corev1.Capabilities
	envVars                []corev1.EnvVar
	serviceAccount         string
	daemonConfigSecretName string
	pullSecretsCreated     bool
}

func (k *KubernetesDriver) assemblePodSpec(pod *corev1.Pod, id string, in *podSpecInputs) error {
	pod.Name = id
	pod.Labels = in.meta.labels

	pod.Spec.ServiceAccountName = in.serviceAccount
	pod.Spec.NodeSelector = in.meta.nodeSelector
	pod.Spec.InitContainers = in.initContainers

	containers, err := getContainers(pod, devsyContainerInputs{
		ImageName:    in.options.Image,
		Entrypoint:   in.options.Entrypoint,
		Args:         in.options.Cmd,
		EnvVars:      in.envVars,
		VolumeMounts: in.volumeMounts,
		Resources:    in.meta.resources,
		Security: securityContextOptions{
			Capabilities:         in.capabilities,
			Privileged:           in.options.Privileged,
			StrictSecurity:       k.options.StrictSecurity,
			AgentSecurityContext: k.options.AgentSecurityContext,
		},
		DaemonConfigSecretName: in.daemonConfigSecretName,
	})
	if err != nil {
		return err
	}
	pod.Spec.Containers = containers

	pod.Spec.Volumes = append(
		getVolumes(pod, id, in.daemonConfigSecretName),
		in.tmpfsVolumes...)
	k.finalizePodSpec(pod, id, in.pullSecretsCreated)
	return nil
}

func (k *KubernetesDriver) resolveWorkspaceMount(
	options *driver.RunOptions,
) (*config.Mount, error) {
	mount := options.WorkspaceMount
	if mount == nil {
		return nil, fmt.Errorf(
			"workspace mount is suppressed; cannot run in Kubernetes without a workspace mount",
		)
	}
	if mount.Target == "" {
		return nil, fmt.Errorf("workspace mount target is empty")
	}
	if k.options.WorkspaceVolumeMount == "" {
		return mount, nil
	}

	// Ensure workspace volume mount option is parent or same dir as workspace mount
	rel, err := filepath.Rel(k.options.WorkspaceVolumeMount, mount.Target)
	switch {
	case err != nil:
		log.Warnf("Relative filepath: %v", err)
	case strings.HasPrefix(rel, ".."):
		log.Warnf(
			"Workspace volume mount needs to be the same as the workspace mount or a parent, skipping option. "+
				"WorkspaceVolumeMount: %s, MountTarget: %s",
			k.options.WorkspaceVolumeMount,
			mount.Target,
		)
	default:
		podMount := *mount
		podMount.Target = k.options.WorkspaceVolumeMount
		mount = &podMount
		log.Debugf("Using workspace volume mount: %s", k.options.WorkspaceVolumeMount)
	}

	return mount, nil
}

func loadPodTemplate(templatePath string) (*corev1.Pod, error) {
	if len(templatePath) > 0 {
		log.Debugf("trying to get pod template manifest from %s", templatePath)
		return getPodTemplate(templatePath)
	}

	return &corev1.Pod{
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}, nil
}

type podContainerInputs struct {
	volumeMounts []corev1.VolumeMount
	tmpfsVolumes []corev1.Volume
	capabilities *corev1.Capabilities
	envVars      []corev1.EnvVar
	daemonConfig string
}

func (k *KubernetesDriver) buildPodContainerInputs(
	options *driver.RunOptions,
	mount *config.Mount,
) *podContainerInputs {
	volumeMounts, tmpfsVolumes := buildVolumeMounts(mount, options)
	capabilities := buildCapabilities(options.CapAdd)
	envVars, daemonConfig := splitEnvVars(options.Env)
	envVars = withAgentInstallPathEnv(envVars, k.options.AgentInstallPath)

	return &podContainerInputs{
		volumeMounts: volumeMounts,
		tmpfsVolumes: tmpfsVolumes,
		capabilities: capabilities,
		envVars:      envVars,
		daemonConfig: daemonConfig,
	}
}

func buildVolumeMounts(
	mount *config.Mount,
	options *driver.RunOptions,
) ([]corev1.VolumeMount, []corev1.Volume) {
	volumeMounts := []corev1.VolumeMount{getVolumeMount(0, mount)}
	var tmpfsVolumes []corev1.Volume
	for idx, mount := range options.Mounts {
		switch mount.Type {
		case driver.MountTypeBind, driver.MountTypeVolume:
			volumeMounts = append(volumeMounts, getVolumeMount(idx+1, mount))
		case driver.MountTypeTmpfs:
			name := fmt.Sprintf("%s-tmpfs-%d", DevContainerName, idx+1)
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      name,
				MountPath: mount.Target,
			})
			tmpfsVolumes = append(tmpfsVolumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: corev1.StorageMediumMemory,
					},
				},
			})
		default:
			log.Warnf(
				"Unsupported mount type %q in mount %q, will skip",
				mount.Type,
				mount.String(),
			)
		}
	}

	return volumeMounts, tmpfsVolumes
}

func buildCapabilities(capAdd []string) *corev1.Capabilities {
	if len(capAdd) == 0 {
		return nil
	}

	capabilities := &corev1.Capabilities{}
	for _, c := range capAdd {
		capabilities.Add = append(capabilities.Add, corev1.Capability(c))
	}

	return capabilities
}

// splitEnvVars converts the env map to EnvVars, extracting the daemon config
// value which is mounted through a secret instead.
func splitEnvVars(env map[string]string) ([]corev1.EnvVar, string) {
	envVars := []corev1.EnvVar{}
	daemonConfig := ""
	for k, v := range env {
		if k == pkgconfig.EnvWorkspaceDaemonConfig {
			daemonConfig = v
			continue
		}
		envVars = append(envVars, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	return envVars, daemonConfig
}

// withAgentInstallPathEnv adds the AGENT_INSTALL_PATH env var to envVars if installPath is non-empty,
// replacing any existing value.
func withAgentInstallPathEnv(envVars []corev1.EnvVar, installPath string) []corev1.EnvVar {
	if installPath == "" {
		return envVars
	}
	for i := range envVars {
		if envVars[i].Name == pkgconfig.EnvAgentPath {
			envVars[i].Value = installPath
			return envVars
		}
	}
	return append(envVars, corev1.EnvVar{Name: pkgconfig.EnvAgentPath, Value: installPath})
}

func (k *KubernetesDriver) ensureServiceAccount(
	ctx context.Context,
	id string,
) (string, error) {
	if k.options.ServiceAccount == "" {
		return "", nil
	}

	serviceAccount := k.options.ServiceAccount
	if err := k.createServiceAccount(ctx, id, serviceAccount); err != nil {
		return "", fmt.Errorf("create service account: %w", err)
	}

	return serviceAccount, nil
}

type podMetadata struct {
	labels       map[string]string
	nodeSelector map[string]string
	resources    corev1.ResourceRequirements
}

func (k *KubernetesDriver) buildPodMetadata(
	pod *corev1.Pod,
	options *driver.RunOptions,
) (*podMetadata, error) {
	labels, err := getLabels(pod, k.options.Labels)
	if err != nil {
		return nil, err
	}
	labels[DevsyWorkspaceUIDLabel] = options.UID

	nodeSelector, err := getNodeSelector(pod, k.options.NodeSelector)
	if err != nil {
		return nil, err
	}

	resources := corev1.ResourceRequirements{}
	if len(pod.Spec.Containers) > 0 {
		resources = pod.Spec.Containers[0].Resources
	}
	if k.options.Resources != "" {
		resources = parseResources(k.options.Resources)
	}

	return &podMetadata{
		labels:       labels,
		nodeSelector: nodeSelector,
		resources:    resources,
	}, nil
}

func (k *KubernetesDriver) ensureDaemonConfig(
	ctx context.Context,
	id, daemonConfig string,
) (string, error) {
	if daemonConfig == "" {
		return "", nil
	}

	daemonConfigSecretName := getDaemonSecretName(id)
	if err := k.EnsureDaemonConfigSecret(ctx, daemonConfigSecretName, daemonConfig); err != nil {
		return "", err
	}

	return daemonConfigSecretName, nil
}

func (k *KubernetesDriver) ensurePullSecrets(
	ctx context.Context,
	id, image string,
) (bool, error) {
	if k.options.KubernetesPullSecretsEnabled != pkgconfig.BoolTrue ||
		k.agentConfig.InjectDockerCredentials != pkgconfig.BoolTrue {
		return false, nil
	}

	return k.EnsurePullSecret(ctx, getPullSecretsName(id), image)
}

func (k *KubernetesDriver) shouldDisableHostUsers() bool {
	return k.options.KubernetesUserNamespaces == pkgconfig.BoolTrue ||
		k.options.StrictSecurity == pkgconfig.BoolTrue ||
		k.options.AgentSecurityContext != ""
}

func (k *KubernetesDriver) finalizePodSpec(pod *corev1.Pod, id string, pullSecretsCreated bool) {
	// avoids a problem where attaching volumes with large repositories would cause an extremely long pod startup time
	// because changing the ownership of all files takes longer than the kubelet expects it to
	if pod.Spec.SecurityContext == nil {
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{
			FSGroupChangePolicy: ptr.To(corev1.FSGroupChangeOnRootMismatch),
		}
	}
	if k.shouldDisableHostUsers() && pod.Spec.HostUsers == nil {
		pod.Spec.HostUsers = new(bool)
	}
	if k.options.KubernetesPullSecretsEnabled == pkgconfig.BoolTrue && pullSecretsCreated {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: getPullSecretsName(id)}}
	}
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever
}

func (k *KubernetesDriver) runPod(
	ctx context.Context,
	id string,
	pod *corev1.Pod,
	timeout time.Duration,
) error {
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
	pod.Annotations[pkgconfig.ClusterAutoscalerSafeToEvictAnnotation] = "false"

	// marshal the pod
	podRaw, err := json.Marshal(pod)
	if err != nil {
		return err
	}

	log.Debugf("Create pod with: %s", string(podRaw))

	// create the pod
	log.Debugf("Create Pod %q", id)
	created, err := k.client.Client().
		CoreV1().
		Pods(k.namespace).
		Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod: %w", err)
	}
	if err := k.rejectAdmittedPod(ctx, created, timeout); err != nil {
		return err
	}

	// wait for pod running
	log.Debugf("Waiting for DevContainer Pod %q to come up", id)
	_, err = k.waitPodRunning(ctx, id)
	if err != nil {
		return err
	}

	return nil
}

type devsyContainerInputs struct {
	ImageName              string
	Entrypoint             string
	Args                   []string
	EnvVars                []corev1.EnvVar
	VolumeMounts           []corev1.VolumeMount
	Resources              corev1.ResourceRequirements
	Security               securityContextOptions
	DaemonConfigSecretName string
}

func getContainers(pod *corev1.Pod, in devsyContainerInputs) ([]corev1.Container, error) {
	daemonConfigSecretName := in.DaemonConfigSecretName
	volumeMounts := in.VolumeMounts
	if daemonConfigSecretName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      DevContainerName + "-daemon-config",
			MountPath: "/var/run/secrets/" + DevContainerName,
		})
	}

	securityContext, err := in.Security.resolve()
	if err != nil {
		return nil, err
	}

	devsyContainer := corev1.Container{
		Name:            DevContainerName,
		Image:           in.ImageName,
		Command:         []string{in.Entrypoint},
		Args:            in.Args,
		Env:             in.EnvVars,
		Resources:       in.Resources,
		VolumeMounts:    volumeMounts,
		SecurityContext: securityContext,
	}
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

	mergeContainer(&devsyContainer, existingDevsyContainer)
	retContainers = append(retContainers, devsyContainer)

	return retContainers, nil
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

	return k.RunDevContainer(ctx, workspaceId, nil)
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
		log.Debugf("Create namespace %q", k.namespace)
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

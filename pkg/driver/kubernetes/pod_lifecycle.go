package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const maxReplaceAttempts = 3

var (
	podPollInterval  = time.Second
	podDeleteTimeout = 2 * time.Minute
)

// errPodChanged means the pod observed before a delete is no longer the pod
// that exists, so the caller must observe again.
var errPodChanged = errors.New("pod changed since it was observed")

type podState int

const (
	podPending podState = iota
	podRunning
	podTerminating
	podTerminal
	podUnknown
)

func classifyPod(pod *corev1.Pod) podState {
	switch {
	case pod.DeletionTimestamp != nil:
		return podTerminating
	case pod.Status.Phase == corev1.PodFailed, pod.Status.Phase == corev1.PodSucceeded:
		return podTerminal
	case pod.Status.Phase == corev1.PodPending:
		return podPending
	case pod.Status.Phase == corev1.PodRunning:
		if devsyContainerTerminated(pod) {
			return podTerminal
		}
		return podRunning
	default:
		return podUnknown
	}
}

func devsyContainerTerminated(pod *corev1.Pod) bool {
	for i := range pod.Status.ContainerStatuses {
		c := &pod.Status.ContainerStatuses[i]
		if c.Name == DevContainerName {
			return IsTerminated(c)
		}
	}
	return false
}

// verifyOwnedPod refuses to touch a pod that Devsy did not create for this
// workspace. Pods that predate the workspace UID label are matched by the
// workspace volume claim.
func verifyOwnedPod(pod *corev1.Pod, id, workspaceUID string) error {
	if pod.Name != id || pod.Labels[DevsyCreatedLabel] != "true" {
		return fmt.Errorf("pod %q exists but was not created by Devsy; refusing to replace it", id)
	}
	if uid := pod.Labels[DevsyWorkspaceUIDLabel]; uid != "" {
		if workspaceUID != "" && uid != workspaceUID {
			return fmt.Errorf(
				"pod %q belongs to workspace %q, not %q; refusing to replace it",
				id, uid, workspaceUID,
			)
		}
		return nil
	}
	if !usesWorkspaceVolume(pod, id) {
		return fmt.Errorf("pod %q does not use the workspace volume; refusing to replace it", id)
	}
	return nil
}

func usesWorkspaceVolume(pod *corev1.Pod, claim string) bool {
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim {
			return true
		}
	}
	return false
}

// reconcileExistingPod makes sure no stale pod blocks creating the workspace
// pod. It reports true when a healthy pod with unchanged options is reused.
func (k *KubernetesDriver) reconcileExistingPod(
	ctx context.Context,
	id string,
	options *driver.RunOptions,
) (bool, error) {
	for range maxReplaceAttempts {
		pod, err := k.getPod(ctx, id)
		if err != nil {
			return false, fmt.Errorf("get pod: %s: %w", id, err)
		}
		if pod == nil {
			return false, nil
		}
		if err := verifyOwnedPod(pod, id, options.UID); err != nil {
			return false, err
		}

		reused, err := k.reconcileDesiredPod(ctx, pod, options)
		if errors.Is(err, errPodChanged) {
			continue
		}
		if err != nil || reused {
			return reused, err
		}
	}
	return false, fmt.Errorf("pod %q kept changing while it was being replaced", id)
}

// reconcileObservedPod handles one observation. It returns (false, nil) after
// removing the pod, so the caller observes again and then creates.
func (k *KubernetesDriver) reconcileObservedPod(
	ctx context.Context,
	id string,
	pod *corev1.Pod,
) (bool, error) {
	switch classifyPod(pod) {
	case podTerminating:
		log.Debugf("Pod %q is terminating, waiting for it to be deleted", id)
		return false, k.waitPodGone(ctx, id, pod.UID)
	case podTerminal:
		log.Infof(
			"Replacing stopped pod %q (phase %s), workspace volume is kept",
			id,
			pod.Status.Phase,
		)
		return false, k.deleteObservedPod(ctx, id, pod.UID)
	case podUnknown:
		return false, fmt.Errorf(
			"pod %q is in phase %q; its node may be unreachable, not replacing it",
			id, pod.Status.Phase,
		)
	}

	if !optionsEqual(storedProviderOptions(pod), k.options) {
		log.Debug("Provider options changed")
		return false, k.deleteObservedPod(ctx, id, pod.UID)
	}
	log.Debugf("Pod %q already exists and nothing changed, skipping update", id)
	if classifyPod(pod) == podPending {
		if err := k.waitPodRunning(ctx, id); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (k *KubernetesDriver) reconcileDesiredPod(
	ctx context.Context,
	pod *corev1.Pod,
	options *driver.RunOptions,
) (bool, error) {
	// A pending pod has not started its containers, so replacing it loses nothing.
	if state := classifyPod(pod); (state == podRunning || state == podPending) &&
		podLaunchDiffers(pod, options) {
		return false, k.deleteObservedPod(ctx, pod.Name, pod.UID)
	}
	reused, err := k.reconcileObservedPod(ctx, pod.Name, pod)
	if err != nil || !reused {
		return reused, err
	}
	// A pod admitted while the timeout was off can hold sidecars that outlive the daemon.
	timeout, err := lifecycleTimeout(options)
	if err != nil {
		return false, fmt.Errorf("read lifecycle policy: %w", err)
	}
	if err := validatePodTopology(pod, timeout); err != nil {
		return false, err
	}
	return true, nil
}

func storedProviderOptions(pod *corev1.Pod) *provider2.ProviderKubernetesDriverConfig {
	existing := &provider2.ProviderKubernetesDriverConfig{}
	if raw := pod.GetAnnotations()[DevsyLastAppliedAnnotation]; raw != "" {
		if err := json.Unmarshal([]byte(raw), existing); err != nil {
			log.Errorf("Error unmarshalling existing provider options, continuing...: %s", err)
		}
	}
	return existing
}

// deleteObservedPod deletes exactly the observed pod instance and waits until
// the API server confirms it is gone. Finalizers are never removed.
func (k *KubernetesDriver) deleteObservedPod(ctx context.Context, id string, uid types.UID) error {
	err := k.client.Client().CoreV1().Pods(k.namespace).Delete(ctx, id, metav1.DeleteOptions{
		GracePeriodSeconds: new(int64(10)),
		Preconditions:      &metav1.Preconditions{UID: &uid},
	})
	switch {
	case err == nil:
	case kerrors.IsNotFound(err):
		return nil
	case kerrors.IsConflict(err):
		return errPodChanged
	default:
		return fmt.Errorf("delete pod %q: %w", id, err)
	}
	return k.waitPodGone(ctx, id, uid)
}

// waitPodGone polls until the pod instance uid is absent. Only NotFound counts
// as absent; any other API error is returned.
func (k *KubernetesDriver) waitPodGone(ctx context.Context, id string, uid types.UID) error {
	err := wait.PollUntilContextTimeout(
		ctx,
		podPollInterval,
		podDeleteTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			pod, err := k.client.Client().
				CoreV1().
				Pods(k.namespace).
				Get(ctx, id, metav1.GetOptions{})
			switch {
			case kerrors.IsNotFound(err):
				return true, nil
			case err != nil:
				return false, fmt.Errorf("check pod %q deletion: %w", id, err)
			}
			return uid != "" && pod.UID != uid, nil
		},
	)
	if err != nil && ctx.Err() == nil && wait.Interrupted(err) {
		return fmt.Errorf("timeout waiting for pod %q to be deleted: %w", id, err)
	}
	return err
}

// ManagedLifecycle reports that the workspace pod's main process is the Devsy
// daemon, which owns inactivity shutdown.
func (k *KubernetesDriver) ManagedLifecycle() bool { return true }

var (
	_ driver.ManagedLifecycleDriver = (*KubernetesDriver)(nil)
	_ driver.ImageRunValidator      = (*KubernetesDriver)(nil)
)

// effectiveTimeout resolves the current provider timeout for a stored shutdown action.
func (k *KubernetesDriver) effectiveTimeout(shutdownAction string) (time.Duration, error) {
	var canonical string
	if k.agentConfig != nil {
		canonical = k.agentConfig.ContainerTimeout
	}
	return agentd.ResolveInactivityTimeout(agentd.TimeoutInputs{
		Canonical:      canonical,
		Legacy:         k.options.InactivityTimeout,
		ShutdownAction: shutdownAction,
		DisableDaemon:  k.disableDaemon,
	})
}

// refreshStoredLifecycle regenerates the lifecycle policy in run options that
// were persisted with the volume, so a replacement pod uses current provider
// values rather than those from when the workspace was created.
func (k *KubernetesDriver) refreshStoredLifecycle(
	options *driver.RunOptions,
) (*driver.RunOptions, error) {
	if options == nil {
		return nil, nil
	}
	stored := options.Env[pkgconfig.EnvWorkspaceDaemonConfig]
	action := ""
	if stored != "" {
		cfg, err := agentd.DecodeDaemonConfig(stored)
		if err != nil {
			return nil, err
		}
		action = cfg.ShutdownAction
	}
	if k.hasShutdownAction {
		action = k.shutdownAction
	}
	timeout, err := k.effectiveTimeout(action)
	if err != nil {
		return nil, err
	}
	lifecycle, err := agentd.ResolveManagedLifecycle(
		timeout, options.Dockerless, options.ImageEntrypoint,
	)
	if err != nil {
		return nil, err
	}
	refreshed, err := agentd.RefreshLifecycle(stored, lifecycle, action)
	if err != nil {
		return nil, err
	}
	out := *options
	out.Env = make(map[string]string, len(options.Env)+1)
	maps.Copy(out.Env, options.Env)
	out.Env[pkgconfig.EnvWorkspaceDaemonConfig] = refreshed
	return &out, nil
}

// lifecycleTimeout returns the positive timeout the generated daemon config
// asks the managed daemon to enforce, or zero.
func lifecycleTimeout(options *driver.RunOptions) (time.Duration, error) {
	raw := options.Env[pkgconfig.EnvWorkspaceDaemonConfig]
	if raw == "" {
		return 0, nil
	}
	cfg, err := agentd.DecodeDaemonConfig(raw)
	if err != nil {
		return 0, err
	}
	if !cfg.Lifecycle.Managed() || cfg.Timeout == "" ||
		cfg.ShutdownAction == config.ShutdownActionNone {
		return 0, nil
	}
	return time.ParseDuration(cfg.Timeout)
}

// validatePodTopology rejects pods in which another regular container could
// keep the pod Running after the Devsy daemon exits. Native sidecars
// (restartable init containers) end with the main container and are allowed.
func validatePodTopology(pod *corev1.Pod, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	hasDevsy := false
	for i := range pod.Spec.Containers {
		hasDevsy = hasDevsy || pod.Spec.Containers[i].Name == DevContainerName
	}
	if !hasDevsy {
		return fmt.Errorf(
			"inactivity timeout %s needs a %q container in the pod",
			timeout,
			DevContainerName,
		)
	}
	return validateNoExtraContainers(pod, timeout)
}

// validateNoExtraContainers is the part of the topology check that can run on
// the pod template, before the devsy container is added to it.
func validateNoExtraContainers(pod *corev1.Pod, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	var extra []string
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name != DevContainerName {
			extra = append(extra, pod.Spec.Containers[i].Name)
		}
	}
	if len(extra) > 0 {
		return fmt.Errorf(
			"inactivity timeout %s cannot stop a pod with extra regular containers (%s) because "+
				"the pod keeps running while they do; move them to native sidecars "+
				"(init containers with restartPolicy Always) or unset the inactivity timeout",
			timeout, strings.Join(extra, ", "),
		)
	}
	return nil
}

// launchCommandChanged reports whether admission rewrote what the devsy
// container executes, which could displace the managed daemon.
func launchCommandChanged(requested, admitted *corev1.Pod) bool {
	want, got := devsyContainer(requested), devsyContainer(admitted)
	if want == nil || got == nil {
		return false
	}
	return !slices.Equal(want.Command, got.Command) || !slices.Equal(want.Args, got.Args)
}

func devsyContainer(pod *corev1.Pod) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == DevContainerName {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

// rejectAdmittedPod removes a freshly created pod whose admitted topology
// violates the stop contract. The workspace volume is kept.
func (k *KubernetesDriver) rejectAdmittedPod(
	ctx context.Context,
	created, requested *corev1.Pod,
	timeout time.Duration,
) error {
	topologyErr := validatePodTopology(created, timeout)
	if topologyErr == nil && timeout > 0 && launchCommandChanged(requested, created) {
		topologyErr = fmt.Errorf(
			"the %q container no longer starts the Devsy daemon as its main process",
			DevContainerName,
		)
	}
	if topologyErr == nil {
		return nil
	}
	cleanup := k.deleteObservedPod(ctx, created.Name, created.UID)
	if cleanup != nil {
		return fmt.Errorf(
			"%w (admission changed the pod; removing it also failed: %w)", topologyErr, cleanup,
		)
	}
	return fmt.Errorf(
		"admission changed the pod and the inactivity timeout cannot be enforced: %w",
		topologyErr,
	)
}

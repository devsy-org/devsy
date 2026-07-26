package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/driver/kubernetes/throttledlogger"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (k *KubernetesDriver) waitPodRunning(ctx context.Context, id string) (*corev1.Pod, error) {
	throttledLogger := throttledlogger.NewThrottledLogger(time.Second * 5)

	timeoutDuration, err := time.ParseDuration(k.options.PodTimeout)
	if err != nil {
		return nil, fmt.Errorf("parse pod timeout: %w", err)
	}

	started := time.Now()
	var pod *corev1.Pod
	err = wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		timeoutDuration,
		true,
		func(ctx context.Context) (bool, error) {
			var err error
			pod, err = k.getPod(ctx, id)
			if err != nil {
				return false, err
			} else if pod == nil {
				return true, nil
			}

			return k.evaluatePodStatus(ctx, pod, &podStatusParams{
				id:              id,
				started:         started,
				throttledLogger: throttledLogger,
			})
		},
	)

	return pod, err
}

type podStatusParams struct {
	id              string
	started         time.Time
	throttledLogger *throttledlogger.ThrottledLogger
}

func (k *KubernetesDriver) evaluatePodStatus(
	ctx context.Context,
	pod *corev1.Pod,
	params *podStatusParams,
) (bool, error) {
	// check pod for problems
	if pod.DeletionTimestamp != nil {
		params.throttledLogger.Infof("Waiting, since pod %q is terminating", params.id)
		return false, nil
	}

	// Let's print all conditions that are false to help people troubleshoot infra issues
	condMsg := podConditionMessage(pod, params.started)

	// check pod status
	if len(pod.Status.ContainerStatuses) < len(pod.Spec.Containers) {
		msg := fmt.Sprintf("Waiting, since pod %q is starting", params.id)
		if condMsg != "" {
			msg += fmt.Sprintf("\n%s", strings.TrimSpace(condMsg))
		}
		params.throttledLogger.Infof("%s", msg)
		return false, nil
	}

	ready, err := checkInitContainerStatuses(pod, params.id, params.throttledLogger)
	if err != nil || !ready {
		return false, err
	}

	return k.checkContainerStatuses(ctx, pod, params.id, params.throttledLogger)
}

func podConditionMessage(pod *corev1.Pod, started time.Time) string {
	if time.Since(started) <= 45*time.Second { // start printing conditions after a delay
		return ""
	}

	var condMsg strings.Builder
	for _, cond := range pod.Status.Conditions {
		if cond.Status != corev1.ConditionFalse {
			continue
		}
		fmt.Fprintf(&condMsg, "Condition %q is %s\n", cond.Type, cond.Status)
		if cond.Reason != "" {
			fmt.Fprintf(&condMsg, "%s Reason: %s\n", cond.Type, cond.Reason)
		}
		if cond.Message != "" {
			fmt.Fprintf(&condMsg, "%s Message: %s\n", cond.Type, cond.Message)
		}
	}

	return condMsg.String()
}

func checkInitContainerStatuses(
	pod *corev1.Pod,
	id string,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	for _, c := range pod.Status.InitContainerStatuses {
		proceed, err := checkInitContainerStatus(pod, id, &c, throttledLogger)
		if err != nil || !proceed {
			return false, err
		}
	}

	return true, nil
}

func checkInitContainerStatus(
	pod *corev1.Pod,
	id string,
	c *corev1.ContainerStatus,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	if IsWaiting(c) {
		return handleInitContainerWaiting(id, c, throttledLogger)
	}

	if IsTerminated(c) && !Succeeded(c) {
		return false, fmt.Errorf(
			"pod %q init container %q is terminated: %s (%s)",
			id,
			c.Name,
			c.State.Terminated.Message,
			c.State.Terminated.Reason,
		)
	}

	container, err := getContainer(pod.Spec.InitContainers, c.Name)
	if err != nil {
		throttledLogger.Infof("Could not find container %q", c.Name)
		return false, err
	}

	return initContainerReady(container, c, id, throttledLogger), nil
}

func handleInitContainerWaiting(
	id string,
	c *corev1.ContainerStatus,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	if IsCritical(c) {
		return false, fmt.Errorf(
			"pod %q init container %q is waiting to start: %s (%s)",
			id,
			c.Name,
			c.State.Waiting.Message,
			c.State.Waiting.Reason,
		)
	}

	logWaiting(throttledLogger, "init container", id, c)
	return false, nil
}

func initContainerReady(
	container *corev1.Container,
	c *corev1.ContainerStatus,
	id string,
	throttledLogger *throttledlogger.ThrottledLogger,
) bool {
	if restartableInitContainer(container.RestartPolicy) {
		if !IsStarted(c) || !IsReady(c) {
			throttledLogger.Infof(
				"Waiting, since pod %q init container %q is not ready yet",
				id,
				c.Name,
			)
			return false
		}
		return true
	}

	if IsRunning(c) {
		throttledLogger.Infof(
			"Waiting, since pod %q init container %q is running",
			id,
			c.Name,
		)
		return false
	}

	return true
}

func (k *KubernetesDriver) checkContainerStatuses(
	ctx context.Context,
	pod *corev1.Pod,
	id string,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	for _, c := range pod.Status.ContainerStatuses {
		proceed, err := k.checkContainerStatus(ctx, id, &c, throttledLogger)
		if err != nil || !proceed {
			return false, err
		}
	}

	return true, nil
}

func (k *KubernetesDriver) checkContainerStatus(
	ctx context.Context,
	id string,
	c *corev1.ContainerStatus,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	// delete succeeded pods
	if IsTerminated(c) && Succeeded(c) {
		// delete pod that is succeeded
		log.Debugf("Delete Pod %q because it is succeeded", id)
		if err := k.waitPodDeleted(ctx, id); err != nil {
			return false, err
		}

		return false, nil
	}

	if IsWaiting(c) {
		return handleContainerWaiting(id, c, throttledLogger)
	}

	if IsTerminated(c) {
		return false, fmt.Errorf(
			"pod %q container %q is terminated: %s (%s)",
			id,
			c.Name,
			c.State.Terminated.Message,
			c.State.Terminated.Reason,
		)
	}

	if !IsReady(c) {
		throttledLogger.Infof(
			"Waiting, since pod %q container %q is not ready yet",
			id,
			c.Name,
		)
		return false, nil
	}

	return true, nil
}

func handleContainerWaiting(
	id string,
	c *corev1.ContainerStatus,
	throttledLogger *throttledlogger.ThrottledLogger,
) (bool, error) {
	if IsCritical(c) {
		return false, fmt.Errorf(
			"pod %q container %q is waiting to start: %s (%s)",
			id,
			c.Name,
			c.State.Waiting.Message,
			c.State.Waiting.Reason,
		)
	}

	logWaiting(throttledLogger, "container", id, c)
	return false, nil
}

func logWaiting(
	throttledLogger *throttledlogger.ThrottledLogger,
	kind, id string,
	c *corev1.ContainerStatus,
) {
	if c.State.Waiting.Message == "" {
		throttledLogger.Infof(
			"Waiting, since pod %q %s %q is waiting to start: %s",
			id,
			kind,
			c.Name,
			c.State.Waiting.Reason,
		)
		return
	}

	throttledLogger.Infof(
		"Waiting, since pod %q %s %q is waiting to start: %s (%s)",
		id,
		kind,
		c.Name,
		c.State.Waiting.Message,
		c.State.Waiting.Reason,
	)
}

func (k *KubernetesDriver) getPod(ctx context.Context, id string) (*corev1.Pod, error) {
	// try to find pod
	pod, err := k.client.Client().CoreV1().Pods(k.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("find container: %w", err)
	}

	return pod, nil
}

func getContainer(containers []corev1.Container, name string) (*corev1.Container, error) {
	for _, c := range containers {
		if c.Name == name {
			return &c, nil
		}
	}

	return nil, fmt.Errorf("cannot find pod container with name %s", name)
}

func restartableInitContainer(p *corev1.ContainerRestartPolicy) bool {
	return p != nil && *p == corev1.ContainerRestartPolicyAlways
}

func (k *KubernetesDriver) waitPodDeleted(ctx context.Context, id string) error {
	err := k.client.Client().CoreV1().Pods(k.namespace).Delete(ctx, id, metav1.DeleteOptions{
		GracePeriodSeconds: &[]int64{10}[0],
	})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("delete pod: %w", err)
	}

	err = wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		time.Minute*2,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := k.client.Client().CoreV1().Pods(k.namespace).Get(ctx, id, metav1.GetOptions{})
			if err != nil {
				return true, nil
			}

			return false, nil
		},
	)
	if err != nil {
		return errors.New("timeout waiting for pod to be deleted")
	}

	return nil
}

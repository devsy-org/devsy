package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// CriticalStatus container status.
var CriticalStatus = map[string]bool{
	"Error":                      true,
	"Unknown":                    true,
	"ImagePullBackOff":           true,
	"CrashLoopBackOff":           true,
	"RunContainerError":          true,
	"ErrImagePull":               true,
	"CreateContainerConfigError": true,
	"InvalidImageName":           true,
}

func WaitForPodReady(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	namespace string,
) (*corev1.Pod, error) {
	// wait until we have a running loft pod
	now := time.Now()
	pod := &corev1.Pod{}
	err := wait.PollUntilContextTimeout(
		ctx,
		time.Second*2,
		Timeout(),
		true,
		func(ctx context.Context) (bool, error) {
			loftPod, found, err := pollLoftPodReady(ctx, kubeClient, namespace, &now)
			if err != nil {
				return false, err
			}
			if loftPod != nil {
				pod = loftPod
			}
			return found, nil
		},
	)
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func pollLoftPodReady(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	namespace string,
	now *time.Time,
) (*corev1.Pod, bool, error) {
	loftPod := latestLoftPod(ctx, kubeClient, namespace, now)
	if loftPod == nil {
		return nil, false, nil
	}

	found := false
	for i := range loftPod.Status.ContainerStatuses {
		containerStatus := loftPod.Status.ContainerStatuses[i]
		ready, err := evalContainerStatus(evalContainerStatusParams{
			ctx:             ctx,
			kubeClient:      kubeClient,
			namespace:       namespace,
			loftPod:         loftPod,
			containerStatus: containerStatus,
			now:             now,
		})
		if err != nil {
			return nil, false, err
		}
		if !ready {
			return nil, false, nil
		}
		if containerStatus.Name == "manager" {
			found = true
		}
	}

	return loftPod, found, nil
}

func latestLoftPod(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	namespace string,
	now *time.Time,
) *corev1.Pod {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=devsy",
	})
	if err != nil {
		log.Warnf("Error trying to retrieve %s pod: %v", pkgconfig.ProductNamePro, err)
		return nil
	}
	if len(pods.Items) == 0 {
		if time.Now().After(now.Add(time.Second * 10)) {
			log.Infof("Still waiting for a %s pod", pkgconfig.ProductNamePro)
			*now = time.Now()
		}
		return nil
	}

	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.After(pods.Items[j].CreationTimestamp.Time)
	})

	return &pods.Items[0]
}

type evalContainerStatusParams struct {
	ctx             context.Context
	kubeClient      kubernetes.Interface
	namespace       string
	loftPod         *corev1.Pod
	containerStatus corev1.ContainerStatus
	now             *time.Time
}

func evalContainerStatus(params evalContainerStatusParams) (bool, error) {
	containerStatus := params.containerStatus
	switch {
	case containerStatus.State.Running != nil && containerStatus.Ready:
		return true, nil
	case containerStatus.State.Terminated != nil ||
		(containerStatus.State.Waiting != nil && CriticalStatus[containerStatus.State.Waiting.Reason]):
		return false, loftPodFailureError(params)
	case containerStatus.State.Waiting != nil && time.Now().After(params.now.Add(time.Second*10)):
		logContainerWaiting(containerStatus)
		*params.now = time.Now()
	}

	return false, nil
}

func loftPodFailureError(params evalContainerStatusParams) error {
	containerStatus := params.containerStatus
	reason := ""
	message := ""
	if containerStatus.State.Terminated != nil {
		reason = containerStatus.State.Terminated.Reason
		message = containerStatus.State.Terminated.Message
	} else if containerStatus.State.Waiting != nil {
		reason = containerStatus.State.Waiting.Reason
		message = containerStatus.State.Waiting.Message
	}

	out, err := params.kubeClient.CoreV1().
		Pods(params.namespace).
		GetLogs(params.loftPod.Name, &corev1.PodLogOptions{
			Container: "manager",
		}).
		Do(params.ctx).
		Raw()
	if err != nil {
		return fmt.Errorf(
			"there seems to be an issue with %s starting up: %s (%s). Reach out to our support at https://devsy.sh/",
			pkgconfig.ProductNamePro,
			message,
			reason,
		)
	}
	if strings.Contains(
		string(out),
		"register instance: Post \"https://license.devsy.sh/register\": dial tcp",
	) {
		return fmt.Errorf(
			"%[1]s logs: \n%[2]v \nThere seems to be an issue with %[1]s starting up. "+
				"Looks like you try to install %[1]s into an air-gapped environment, "+
				"reach out to our support at https://devsy.sh/ for an offline license",
			pkgconfig.ProductNamePro,
			string(out),
		)
	}

	return fmt.Errorf(
		"%[1]s logs: \n%[2]v \nThere seems to be an issue with %[1]s starting up: %[3]s (%[4]s). "+
			"Reach out to our support at https://devsy.sh/",
		pkgconfig.ProductNamePro,
		string(out),
		message,
		reason,
	)
}

func logContainerWaiting(containerStatus corev1.ContainerStatus) {
	switch {
	case containerStatus.State.Waiting.Message != "":
		log.Infof(
			"Keep waiting, %s container is still starting up: %s (%s)",
			pkgconfig.ProductNamePro,
			containerStatus.State.Waiting.Message,
			containerStatus.State.Waiting.Reason,
		)
	case containerStatus.State.Waiting.Reason != "":
		log.Infof(
			"Keep waiting, %s container is still starting up: %s",
			pkgconfig.ProductNamePro,
			containerStatus.State.Waiting.Reason,
		)
	default:
		log.Infof(
			"Keep waiting, %s container is still starting up",
			pkgconfig.ProductNamePro,
		)
	}
}

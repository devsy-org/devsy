package pro

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	devsyclientset "github.com/devsy-org/api/pkg/clientset/versioned"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// uninstallTarget identifies the kube context and namespace Devsy Pro is installed in.
type uninstallTarget struct {
	kubeContext string
	namespace   string
}

// Helm flags used when uninstalling Devsy Pro.
const (
	helmKubeContextFlag = "--kube-context"
	helmNamespaceFlag   = "--namespace"
)

func uninstall(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	restConfig *rest.Config,
	target uninstallTarget,
) error {
	if err := runHelmUninstall(ctx, kubeClient, target.kubeContext, target.namespace); err != nil {
		return err
	}

	if err := cleanupPlatformResources(ctx, kubeClient, restConfig, target.namespace); err != nil {
		return err
	}

	fmt.Fprint(os.Stderr, "\n")
	log.Info("uninstalled Devsy Pro")
	fmt.Fprint(os.Stderr, "\n")

	return nil
}

func runHelmUninstall(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	kubeContext, namespace string,
) error {
	releaseName, err := resolveReleaseName(ctx, kubeClient, namespace)
	if err != nil {
		return err
	}

	args := []string{
		"uninstall",
		releaseName,
		helmKubeContextFlag,
		kubeContext,
		helmNamespaceFlag,
		namespace,
	}
	log.Infof("Executing command: helm %s", strings.Join(args, " "))
	helmCmd := exec.CommandContext(ctx, "helm", args...) // #nosec G204 -- internally constructed
	output, err := helmCmd.CombinedOutput()
	if err != nil {
		log.Errorf("error during helm command: %s (%v)", string(output), err)
	}

	return nil
}

func cleanupPlatformResources(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	restConfig *rest.Config,
	namespace string,
) error {
	apiRegistrationClient, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	err = deleteIgnoreNotFound(func() error {
		return apiRegistrationClient.ApiregistrationV1().
			APIServices().
			Delete(ctx, "v1.management.devsy.sh", metav1.DeleteOptions{})
	})
	if err != nil {
		return err
	}

	if err := deleteUser(ctx, restConfig, "admin"); err != nil {
		return err
	}

	return deleteRemainingAgentResources(ctx, kubeClient, apiRegistrationClient, namespace)
}

func deleteRemainingAgentResources(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	apiRegistrationClient *clientset.Clientset,
	namespace string,
) error {
	return deleteIgnoreNotFound(
		func() error {
			return kubeClient.CoreV1().
				Secrets(namespace).
				Delete(ctx, "loft-user-secret-admin", metav1.DeleteOptions{})
		},
		func() error {
			return kubeClient.CoreV1().
				Secrets(namespace).
				Delete(ctx, DevsyRouterDomainSecret, metav1.DeleteOptions{})
		},
		func() error {
			return kubeClient.AdmissionregistrationV1().
				ValidatingWebhookConfigurations().
				Delete(ctx, "loft-agent", metav1.DeleteOptions{})
		},
		func() error {
			return apiRegistrationClient.ApiregistrationV1().
				APIServices().
				Delete(ctx, "v1alpha1.tenancy.kiosk.sh", metav1.DeleteOptions{})
		},
		func() error {
			return apiRegistrationClient.ApiregistrationV1().
				APIServices().
				Delete(ctx, "v1.cluster.devsy.sh", metav1.DeleteOptions{})
		},
		func() error {
			return kubeClient.CoreV1().
				ConfigMaps(namespace).
				Delete(ctx, "loft-agent-controller", metav1.DeleteOptions{})
		},
		func() error {
			return kubeClient.CoreV1().
				ConfigMaps(namespace).
				Delete(ctx, "loft-applied-defaults", metav1.DeleteOptions{})
		},
	)
}

func resolveReleaseName(
	ctx context.Context,
	kubeClient kubernetes.Interface,
	namespace string,
) (string, error) {
	releaseName := config.PlatformReleaseName
	deploy, err := kubeClient.AppsV1().
		Deployments(namespace).
		Get(ctx, defaultDeploymentName, metav1.GetOptions{})
	if err != nil && !kerrors.IsNotFound(err) {
		return "", err
	} else if deploy != nil && deploy.Labels != nil && deploy.Labels["release"] != "" {
		releaseName = deploy.Labels["release"]
	}

	return releaseName, nil
}

func deleteIgnoreNotFound(deletes ...func() error) error {
	for _, del := range deletes {
		if err := del(); err != nil && !kerrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func deleteUser(ctx context.Context, restConfig *rest.Config, name string) error {
	devsyClient, err := devsyclientset.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	user, err := devsyClient.StorageV1().Users().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil
	} else if len(user.Finalizers) > 0 {
		user.Finalizers = nil
		_, err = devsyClient.StorageV1().Users().Update(ctx, user, metav1.UpdateOptions{})
		if err != nil {
			if kerrors.IsConflict(err) {
				return deleteUser(ctx, restConfig, name)
			}

			return err
		}
	}

	err = devsyClient.StorageV1().Users().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !kerrors.IsNotFound(err) {
		return err
	}

	return nil
}

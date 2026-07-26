package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/platform/annotations"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/devsy-org/devsy/pkg/platform/project"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var configTTL time.Duration = time.Hour * 24 * 90 // 90 days

// NewInstanceKubeConfig creates a KubeConfig (clientcmdapi.Config) based for either a space instance or virtual cluster instance.
// We return the config as byte slice to ensure correct handling and formatting through the `clientcmd` methods.
func NewInstanceKubeConfig(
	ctx context.Context,
	platformOptions *devsy.PlatformOptions,
) ([]byte, error) {
	if platformOptions == nil || platformOptions.Kubernetes == nil {
		return nil, nil
	}

	skip, err := validateKubeConfigOptions(platformOptions)
	if err != nil {
		return nil, err
	}
	if skip {
		return nil, nil
	}

	k8sOpts := platformOptions.Kubernetes
	baseClient := client.NewClientFromConfig(&client.Config{
		AccessKey: platformOptions.UserAccessKey,
		Host:      "https://" + platformOptions.PlatformHost,
		Insecure:  true,
	})
	if err := baseClient.RefreshSelf(ctx); err != nil {
		return nil, fmt.Errorf("refresh self: %w", err)
	}

	kubeConfig, err := kubeConfigForTarget(ctx, baseClient, k8sOpts)
	if err != nil {
		return nil, err
	}

	return clientcmd.Write(*kubeConfig)
}

func validateKubeConfigOptions(platformOptions *devsy.PlatformOptions) (bool, error) {
	if platformOptions.UserAccessKey == "" {
		return false, fmt.Errorf("user access key missing")
	}
	if platformOptions.PlatformHost == "" {
		return false, fmt.Errorf("platform host is missing")
	}
	k8sOpts := platformOptions.Kubernetes
	if k8sOpts.SpaceName == "" && k8sOpts.VirtualClusterName == "" {
		// nothing to do here
		return true, nil
	}
	if k8sOpts.SpaceName != "" && k8sOpts.VirtualClusterName != "" {
		return false, fmt.Errorf("cannot use virtual cluster and space instance together")
	}
	if k8sOpts.Namespace == "" {
		return false, fmt.Errorf("namespace missing")
	}

	return false, nil
}

func kubeConfigForTarget(
	ctx context.Context,
	baseClient client.Client,
	k8sOpts *devsy.Kubernetes,
) (*clientcmdapi.Config, error) {
	if k8sOpts.SpaceName != "" {
		return kubeConfigForSpaceInstance(ctx, baseClient, k8sOpts.SpaceName, k8sOpts.Namespace)
	}

	return kubeConfigForVirtualClusterInstance(
		ctx,
		baseClient,
		k8sOpts.VirtualClusterName,
		k8sOpts.Namespace,
	)
}

func kubeConfigForSpaceInstance(
	ctx context.Context,
	baseClient client.Client,
	spaceInstanceName string,
	namespace string,
) (*clientcmdapi.Config, error) {
	projectName := project.ProjectFromNamespace(namespace)
	managementClient, err := baseClient.Management()
	if err != nil {
		return nil, err
	}

	spaceInstance, err := managementClient.Loft().
		ManagementV1().
		SpaceInstances(namespace).
		Get(ctx, spaceInstanceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get space instance: %w", err)
	}

	// find cluster by clusterRef
	hostCluster, err := findHostCluster(ctx, baseClient, projectName, spaceInstance.Spec.ClusterRef)
	if err != nil {
		return nil, fmt.Errorf("find host cluster: %w", err)
	}

	scope := &storagev1.AccessKeyScope{
		Spaces: []storagev1.AccessKeyScopeSpace{{
			Project: projectName,
			Space:   spaceInstance.Name,
		}},
	}
	ttl := int64(configTTL.Seconds())

	// direct cluster access?
	if hostCluster.GetAnnotations()[annotations.LoftDirectClusterEndpoint] != "" {
		tok := &managementv1.DirectClusterEndpointToken{
			Spec: managementv1.DirectClusterEndpointTokenSpec{
				Scope: scope,
				TTL:   ttl,
			},
		}
		directClusterEndpointToken, err := managementClient.Loft().
			ManagementV1().
			DirectClusterEndpointTokens().
			Create(ctx, tok, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("create direct cluster endpoint token: %w", err)
		}

		directClusterEndpoint := hostCluster.GetAnnotations()[annotations.LoftDirectClusterEndpoint]
		host := fmt.Sprintf(
			"https://%s/kubernetes/project/%s/space/%s",
			directClusterEndpoint,
			projectName,
			spaceInstance.Name,
		)

		return newKubeConfig(
			host,
			directClusterEndpointToken.Status.Token,
			spaceInstance.Spec.ClusterRef.Namespace,
			true,
		), nil
	}

	// access through management cluster + access key
	key := &managementv1.OwnedAccessKey{
		Spec: managementv1.OwnedAccessKeySpec{
			AccessKeySpec: storagev1.AccessKeySpec{
				User:  baseClient.Self().Status.User.Name,
				Scope: scope,
				TTL:   ttl,
				DisplayName: fmt.Sprintf(
					"Kube Config for Space %s/%s",
					spaceInstance.Namespace,
					spaceInstance.Name,
				),
			},
		},
	}
	ownedAccessKey, err := managementClient.Loft().
		ManagementV1().
		OwnedAccessKeys().
		Create(ctx, key, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create access key: %w", err)
	}
	hostName := strings.TrimPrefix(
		strings.TrimPrefix(baseClient.Config().Host, "https://"),
		"https://",
	)
	host := fmt.Sprintf(
		"https://%s/kubernetes/project/%s/space/%s",
		hostName,
		projectName,
		spaceInstance.Name,
	)

	return newKubeConfig(
		host,
		ownedAccessKey.Spec.Key,
		spaceInstance.Spec.ClusterRef.Namespace,
		true,
	), nil
}

func kubeConfigForVirtualClusterInstance(
	ctx context.Context,
	baseClient client.Client,
	virtualClusterInstanceName string,
	namespace string,
) (*clientcmdapi.Config, error) {
	projectName := project.ProjectFromNamespace(namespace)
	managementClient, err := baseClient.Management()
	if err != nil {
		return nil, err
	}

	virtualClusterInstance, err := managementClient.Loft().
		ManagementV1().
		VirtualClusterInstances(namespace).
		Get(ctx, virtualClusterInstanceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get virtual cluster instance: %w", err)
	}

	scope := &storagev1.AccessKeyScope{
		VirtualClusters: []storagev1.AccessKeyScopeVirtualCluster{{
			Project:        projectName,
			VirtualCluster: virtualClusterInstance.Name,
		}},
	}
	req := vClusterKubeConfigRequest{
		ctx:              ctx,
		managementClient: managementClient,
		namespace:        namespace,
		projectName:      projectName,
		scope:            scope,
		ttl:              int64(configTTL.Seconds()),
		instance:         virtualClusterInstance,
	}

	// direct virtual cluster ingress access?
	virtualCluster := virtualClusterInstance.Status.VirtualCluster
	if virtualCluster != nil && virtualCluster.AccessPoint.Ingress.Enabled {
		return directIngressKubeConfig(req)
	}

	// find cluster by clusterRef
	hostCluster, err := findHostCluster(
		ctx,
		baseClient,
		projectName,
		virtualClusterInstance.Spec.ClusterRef.ClusterRef,
	)
	if err != nil {
		return nil, fmt.Errorf("find host cluster: %w", err)
	}

	// direct cluster access?
	if hostCluster.GetAnnotations()[annotations.LoftDirectClusterEndpoint] != "" {
		return directClusterEndpointKubeConfig(req, hostCluster)
	}

	// access through management cluster + access key
	key := &managementv1.OwnedAccessKey{
		Spec: managementv1.OwnedAccessKeySpec{
			AccessKeySpec: storagev1.AccessKeySpec{
				User:  baseClient.Self().Status.User.Name,
				Scope: scope,
				TTL:   req.ttl,
				DisplayName: fmt.Sprintf(
					"Kube Config for Virtual Cluster %s/%s",
					virtualClusterInstance.Namespace,
					virtualClusterInstance.Name,
				),
			},
		},
	}
	ownedAccessKey, err := managementClient.Loft().
		ManagementV1().
		OwnedAccessKeys().
		Create(ctx, key, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create access key: %w", err)
	}
	hostName := strings.TrimPrefix(
		strings.TrimPrefix(baseClient.Config().Host, "https://"),
		"https://",
	)
	host := fmt.Sprintf(
		"https://%s/kubernetes/project/%s/virtualcluster/%s",
		hostName,
		projectName,
		virtualClusterInstance.Name,
	)

	return newKubeConfig(
		host,
		ownedAccessKey.Spec.Key,
		virtualClusterInstance.Spec.ClusterRef.Namespace,
		true,
	), nil
}

type vClusterKubeConfigRequest struct {
	ctx              context.Context
	managementClient kube.Interface
	namespace        string
	projectName      string
	scope            *storagev1.AccessKeyScope
	ttl              int64
	instance         *managementv1.VirtualClusterInstance
}

func directIngressKubeConfig(req vClusterKubeConfigRequest) (*clientcmdapi.Config, error) {
	certTTL := int32(req.ttl) // #nosec G115 -- ttl from fixed 90-day constant, no overflow
	config := &managementv1.VirtualClusterInstanceKubeConfig{
		Spec: managementv1.VirtualClusterInstanceKubeConfigSpec{
			CertificateTTL: &certTTL,
		},
	}
	directVirtualClusterKubeConfig, err := req.managementClient.Loft().
		ManagementV1().
		VirtualClusterInstances(req.namespace).
		GetKubeConfig(req.ctx, req.instance.Name, config, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("get virtual cluster kube config: %w", err)
	}

	kubeConfig, err := clientcmd.Load([]byte(directVirtualClusterKubeConfig.Status.KubeConfig))
	if err != nil {
		return nil, err
	}

	return kubeConfig, nil
}

func directClusterEndpointKubeConfig(
	req vClusterKubeConfigRequest,
	hostCluster managementv1.Cluster,
) (*clientcmdapi.Config, error) {
	tok := &managementv1.DirectClusterEndpointToken{
		Spec: managementv1.DirectClusterEndpointTokenSpec{
			Scope: req.scope,
			TTL:   req.ttl,
		},
	}
	directClusterEndpointToken, err := req.managementClient.Loft().
		ManagementV1().
		DirectClusterEndpointTokens().
		Create(req.ctx, tok, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create direct cluster endpoint token: %w", err)
	}

	directClusterEndpoint := hostCluster.GetAnnotations()[annotations.LoftDirectClusterEndpoint]
	host := fmt.Sprintf(
		"https://%s/kubernetes/project/%s/virtualcluster/%s",
		directClusterEndpoint,
		req.projectName,
		req.instance.Name,
	)

	return newKubeConfig(
		host,
		directClusterEndpointToken.Status.Token,
		req.instance.Spec.ClusterRef.Namespace,
		true,
	), nil
}

func findHostCluster(
	ctx context.Context,
	baseClient client.Client,
	projectName string,
	clusterRef storagev1.ClusterRef,
) (managementv1.Cluster, error) {
	managementClient, err := baseClient.Management()
	if err != nil {
		return managementv1.Cluster{}, err
	}
	projectClusters, err := managementClient.Loft().
		ManagementV1().
		Projects().
		ListClusters(ctx, projectName, metav1.GetOptions{})
	if err != nil {
		return managementv1.Cluster{}, fmt.Errorf("get project clusters: %w", err)
	}

	for _, cluster := range projectClusters.Clusters {
		if clusterRef.Cluster == cluster.GetName() {
			return cluster, nil
		}
	}

	return managementv1.Cluster{}, nil
}

func newKubeConfig(host, token, namespace string, insecure bool) *clientcmdapi.Config {
	contextName := "loft"
	kubeConfig := clientcmdapi.NewConfig()
	kubeConfig.Contexts = map[string]*clientcmdapi.Context{
		contextName: {
			Cluster:   contextName,
			AuthInfo:  contextName,
			Namespace: namespace,
		},
	}
	kubeConfig.Clusters = map[string]*clientcmdapi.Cluster{
		contextName: {
			Server:                host,
			InsecureSkipTLSVerify: insecure,
		},
	}
	kubeConfig.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		contextName: {
			Token: token,
		},
	}
	kubeConfig.CurrentContext = contextName

	return kubeConfig
}

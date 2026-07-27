package cluster

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/devsy-org/devsy/pkg/survey"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

type ClusterCmd struct {
	*proflags.GlobalFlags

	Namespace        string
	ServiceAccount   string
	DisplayName      string
	KubeContext      string
	Insecure         bool
	Wait             bool
	HelmChartPath    string
	HelmChartVersion string
	HelmSet          []string
	HelmValues       []string
	Host             string
}

// NewAddCmd creates a new command.
func NewAddCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &ClusterCmd{
		GlobalFlags: globalFlags,
	}

	c := &cobra.Command{
		Use:   "add <cluster-name>",
		Short: "Add current cluster to Devsy Pro",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	cliflags.Add(
		c,
		cliflags.String(
			&cmd.Namespace,
			names.Namespace,
			"loft",
			"The namespace to generate the service account in. The namespace will be created if it does not exist",
		),
		cliflags.String(
			&cmd.ServiceAccount,
			names.ServiceAccount,
			"loft-admin",
			"The service account name to create",
		),
		cliflags.String(
			&cmd.DisplayName,
			names.DisplayName,
			"",
			"The display name to show in the UI for this cluster",
		),
		cliflags.Bool(
			&cmd.Wait,
			names.Wait,
			false,
			"If true, will wait until the cluster is initialized",
		),
		cliflags.Bool(
			&cmd.Insecure,
			names.Insecure,
			false,
			"If true, deploys the agent in insecure mode",
		),
		cliflags.String(
			&cmd.HelmChartVersion,
			names.HelmChartVersion,
			"",
			"The agent chart version to deploy",
		),
		cliflags.String(&cmd.HelmChartPath, names.HelmChartPath, "", "The agent chart to deploy"),
		cliflags.StringArray(
			&cmd.HelmSet,
			names.HelmSet,
			[]string{},
			"Extra helm values for the agent chart",
		),
		cliflags.StringArray(
			&cmd.HelmValues,
			names.HelmValues,
			[]string{},
			"Extra helm values for the agent chart",
		),
		cliflags.String(
			&cmd.KubeContext,
			names.KubeContext,
			"",
			"The kube context to use for installation",
		),
		cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"),
	)
	proflags.BindEnv(c.Flags(), names.Host)

	return c
}

func (cmd *ClusterCmd) Run(ctx context.Context, args []string) error {
	clusterName := args[0]

	setup, err := cmd.setupCluster(ctx, clusterName)
	if err != nil {
		return err
	}
	managementClient := setup.managementClient

	helmArgs := cmd.buildHelmArgs(setup.chartVersion, setup.accessKey)

	secretsFile, err := writeAgentSecretsFile(setup.accessKey)
	if err != nil {
		return err
	}
	if secretsFile != "" {
		defer func() { _ = os.Remove(secretsFile) }()
		helmArgs = append(helmArgs, "--values", secretsFile)
	}

	clientset, err := loadKubeClientset(cmd.KubeContext)
	if err != nil {
		return err
	}

	if err := installAgent(ctx, clientset, cmd.Namespace, helmArgs); err != nil {
		return err
	}

	if cmd.Wait {
		if err := waitForClusterInitialized(ctx, managementClient, clusterName); err != nil {
			return err
		}
	}

	log.Infof("added cluster: cluster=%s", clusterName)

	return nil
}

type clusterSetup struct {
	managementClient kube.Interface
	accessKey        *managementv1.ClusterAccessKey
	chartVersion     string
}

type createClusterParams struct {
	clusterName string
	user        string
	team        string
}

func (cmd *ClusterCmd) setupCluster(
	ctx context.Context,
	clusterName string,
) (clusterSetup, error) {
	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return clusterSetup{}, err
	}

	cmd.Host, err = ensureHost(devsyConfig, cmd.Host)
	if err != nil {
		return clusterSetup{}, err
	}

	baseClient, err := platform.InitClientFromHost(ctx, devsyConfig, cmd.Host)
	if err != nil {
		return clusterSetup{}, err
	}

	managementClient, err := baseClient.Management()
	if err != nil {
		return clusterSetup{}, err
	}

	devsyVersion, err := baseClient.Version()
	if err != nil {
		return clusterSetup{}, fmt.Errorf("get pro version: %w", err)
	}

	user, team := getUserOrTeam(baseClient)

	if err := cmd.createClusterResource(ctx, managementClient, createClusterParams{
		clusterName: clusterName,
		user:        user,
		team:        team,
	}); err != nil {
		return clusterSetup{}, err
	}

	accessKey, err := managementClient.Loft().
		ManagementV1().
		Clusters().
		GetAccessKey(ctx, clusterName, metav1.GetOptions{})
	if err != nil {
		return clusterSetup{}, fmt.Errorf("get cluster access key: %w", err)
	}

	return clusterSetup{
		managementClient: managementClient,
		accessKey:        accessKey,
		chartVersion:     devsyVersion.Version,
	}, nil
}

func (cmd *ClusterCmd) createClusterResource(
	ctx context.Context,
	managementClient kube.Interface,
	params createClusterParams,
) error {
	_, err := managementClient.Loft().ManagementV1().Clusters().Create(ctx, &managementv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: params.clusterName,
		},
		Spec: managementv1.ClusterSpec{
			ClusterSpec: storagev1.ClusterSpec{
				DisplayName: cmd.DisplayName,
				Owner: &storagev1.UserOrTeam{
					User: params.user,
					Team: params.team,
				},
				NetworkPeer: true,
				Access:      getAccess(params.user, params.team),
			},
		},
	}, metav1.CreateOptions{})
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("create cluster: %w", err)
	}

	return nil
}

func (cmd *ClusterCmd) buildHelmArgs(
	chartVersion string,
	accessKey *managementv1.ClusterAccessKey,
) []string {
	helmArgs := []string{"upgrade", "loft"}

	if os.Getenv("DEVELOPMENT") == "true" {
		helmArgs = []string{
			"upgrade",
			"--install",
			"loft",
			cmp.Or(os.Getenv("DEVELOPMENT_CHART_DIR"), "./chart"),
			"--create-namespace",
			"--namespace",
			cmd.Namespace,
			"--set",
			"agentOnly=true",
			"--set",
			"image=" + cmp.Or(
				os.Getenv("DEVELOPMENT_IMAGE"),
				"ghcr.io/devsy-org/enterprise:release-test",
			),
		}
	} else {
		helmArgs = cmd.appendReleaseArgs(helmArgs, chartVersion)
	}

	for _, set := range cmd.HelmSet {
		helmArgs = append(helmArgs, "--set", set)
	}
	for _, values := range cmd.HelmValues {
		helmArgs = append(helmArgs, "--values", values)
	}

	return cmd.appendAccessKeyArgs(helmArgs, accessKey)
}

func (cmd *ClusterCmd) appendReleaseArgs(helmArgs []string, chartVersion string) []string {
	if cmd.HelmChartPath != "" {
		helmArgs = append(helmArgs, cmd.HelmChartPath)
	} else {
		helmArgs = append(helmArgs, "loft", "--repo", "https://charts.devsy.sh")
	}

	if chartVersion != "" {
		helmArgs = append(helmArgs, "--version", chartVersion)
	}

	if cmd.HelmChartVersion != "" {
		helmArgs = append(helmArgs, "--version", cmd.HelmChartVersion)
	}

	return append(
		helmArgs,
		"--install",
		"--create-namespace",
		"--namespace",
		cmd.Namespace,
		"--set",
		"agentOnly=true",
	)
}

func (cmd *ClusterCmd) appendAccessKeyArgs(
	helmArgs []string,
	accessKey *managementv1.ClusterAccessKey,
) []string {
	if accessKey.DevsyHost != "" {
		helmArgs = append(helmArgs, "--set", "url="+accessKey.DevsyHost)
	}
	if cmd.Insecure || accessKey.Insecure {
		helmArgs = append(helmArgs, "--set", "insecureSkipVerify=true")
	}
	if cmd.Wait {
		helmArgs = append(helmArgs, "--wait")
	}
	if cmd.KubeContext != "" {
		helmArgs = append(helmArgs, "--kube-context", cmd.KubeContext)
	}

	return helmArgs
}

func loadKubeClientset(kubeContext string) (*kubernetes.Clientset, error) {
	kubeClientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	if kubeContext != "" {
		kubeConfig, err := kubeClientConfig.RawConfig()
		if err != nil {
			return nil, fmt.Errorf(
				"there is an error loading your current kube config (%w), make sure you have access "+
					"to a kubernetes cluster and the command `kubectl get namespaces` is working",
				err,
			)
		}

		kubeClientConfig = clientcmd.NewNonInteractiveClientConfig(
			kubeConfig,
			kubeContext,
			&clientcmd.ConfigOverrides{},
			clientcmd.NewDefaultClientConfigLoadingRules(),
		)
	}

	config, err := kubeClientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf(
			"there is an error loading your current kube config (%w), make sure you have access "+
				"to a kubernetes cluster and the command `kubectl get namespaces` is working",
			err,
		)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kube client: %w", err)
	}

	return clientset, nil
}

func writeAgentSecretsFile(accessKey *managementv1.ClusterAccessKey) (string, error) {
	secrets := map[string]string{}
	if accessKey.AccessKey != "" {
		secrets["token"] = accessKey.AccessKey
	}
	if accessKey.CaCert != "" {
		secrets["additionalCA"] = accessKey.CaCert
	}
	if len(secrets) == 0 {
		return "", nil
	}

	data, err := yaml.Marshal(secrets)
	if err != nil {
		return "", fmt.Errorf("marshal agent secret values: %w", err)
	}

	f, err := os.CreateTemp("", "devsy-agent-values-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create agent secret values file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write agent secret values file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("close agent secret values file: %w", err)
	}

	return f.Name(), nil
}

func installAgent(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	namespace string,
	helmArgs []string,
) error {
	errChan := make(chan error)

	go func() {
		helmCmd := exec.CommandContext(ctx, "helm", helmArgs...)

		helmCmd.Stdout = log.Writer(log.LevelDebug)
		helmCmd.Stderr = log.Writer(log.LevelDebug)
		helmCmd.Stdin = os.Stdin

		log.Info("Installing agent")
		log.Debugf("Running helm command: %v", helmCmd.Args)

		if err := helmCmd.Run(); err != nil {
			errChan <- fmt.Errorf("failed to install chart: %w", err)
		}

		close(errChan)
	}()

	_, err := platform.WaitForPodReady(ctx, clientset, namespace)
	if err = errors.Join(err, <-errChan); err != nil {
		return fmt.Errorf("wait for pod: %w", err)
	}

	return nil
}

func waitForClusterInitialized(
	ctx context.Context,
	managementClient kube.Interface,
	clusterName string,
) error {
	log.Info("Waiting for the cluster to be initialized")
	waitErr := wait.PollUntilContextTimeout(
		ctx,
		time.Second,
		5*time.Minute,
		false,
		func(ctx context.Context) (done bool, err error) {
			clusterInstance, err := managementClient.Loft().
				ManagementV1().
				Clusters().
				Get(ctx, clusterName, metav1.GetOptions{})
			if err != nil && !kerrors.IsNotFound(err) {
				return false, err
			}

			return clusterInstance != nil &&
				clusterInstance.Status.Phase == storagev1.ClusterStatusPhaseInitialized, nil
		},
	)
	if waitErr != nil {
		return fmt.Errorf("get cluster: %w", waitErr)
	}

	return nil
}

func ensureHost(devsyConfig *config.Config, host string) (string, error) {
	if host != "" {
		return host, nil
	}

	proInstances, err := workspace.ListProInstances(devsyConfig)
	if err != nil {
		return "", fmt.Errorf("list pro instances: %w", err)
	}
	options := []string{}
	for _, pro := range proInstances {
		options = append(options, pro.Host)
	}
	h, err := log.QuestionDefault(&survey.QuestionOptions{
		Question:     "Select Pro instance to connect your cluster to",
		Options:      options,
		DefaultValue: options[0],
	})
	if err != nil {
		return "", fmt.Errorf("select pro instance: %w", err)
	}

	return h, nil
}

func getUserOrTeam(baseClient client.Client) (string, string) {
	var user, team string

	self := baseClient.Self()
	userName := self.Status.User
	teamName := self.Status.Team

	if userName != nil {
		user = userName.Name
	} else {
		team = teamName.Name
	}

	return user, team
}

func getAccess(user, team string) []storagev1.Access {
	access := []storagev1.Access{
		{
			Verbs:        []string{"*"},
			Subresources: []string{"*"},
		},
	}

	if team != "" {
		access[0].Teams = []string{team}
	} else {
		access[0].Users = []string{user}
	}

	return access
}

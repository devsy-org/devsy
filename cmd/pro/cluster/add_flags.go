package cluster

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
)

func (cmd *ClusterCmd) registerFlags(c *cobra.Command) {
	cmd.registerIdentityFlags(c)
	cmd.registerBehaviorFlags(c)
	cmd.registerHelmFlags(c)
	cmd.registerClusterFlags(c)
}

func (cmd *ClusterCmd) registerIdentityFlags(c *cobra.Command) {
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
	)
}

func (cmd *ClusterCmd) registerBehaviorFlags(c *cobra.Command) {
	cliflags.Add(
		c,
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
	)
}

func (cmd *ClusterCmd) registerHelmFlags(c *cobra.Command) {
	cliflags.Add(
		c,
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
	)
}

func (cmd *ClusterCmd) registerClusterFlags(c *cobra.Command) {
	cliflags.Add(
		c,
		cliflags.String(
			&cmd.KubeContext,
			names.KubeContext,
			"",
			"The kube context to use for installation",
		),
		cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"),
	)
	proflags.BindEnv(c.Flags(), names.Host)
}

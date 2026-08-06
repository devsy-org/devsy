package pro

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
)

func (cmd *StartCmd) registerFlags(startCmd *cobra.Command) {
	cmd.registerDockerFlags(startCmd)
	cmd.registerClusterFlags(startCmd)
	cmd.registerChartFlags(startCmd)
	cmd.registerAuthFlags(startCmd)
	cmd.registerLifecycleFlags(startCmd)
}

func (cmd *StartCmd) registerDockerFlags(startCmd *cobra.Command) {
	cliflags.Add(
		startCmd,
		cliflags.Bool(
			&cmd.Docker,
			names.Docker,
			false,
			"If enabled will try to deploy Devsy Pro to the local docker installation.",
		),
		cliflags.String(&cmd.DockerImage, names.DockerImage, "", "The docker image to install."),
		cliflags.StringArray(&cmd.DockerArgs, names.DockerArg, []string{}, "Extra docker args"),
	)
}

func (cmd *StartCmd) registerClusterFlags(startCmd *cobra.Command) {
	cliflags.Add(
		startCmd,
		cliflags.String(
			&cmd.Context,
			names.Context,
			"",
			"The kube context to use for installation",
		),
		cliflags.String(
			&cmd.Namespace,
			names.Namespace,
			config.ProReleaseName,
			"The namespace to install into",
		),
		cliflags.String(
			&cmd.Host,
			names.Host,
			"",
			"Provide a hostname to enable ingress and configure its hostname",
		),
	)

	proflags.BindEnv(startCmd.Flags(), names.Host)
}

func (cmd *StartCmd) registerChartFlags(startCmd *cobra.Command) {
	cliflags.Add(
		startCmd,
		cliflags.String(&cmd.Version, names.Version, "", "The version to install"),
		cliflags.String(
			&cmd.Values,
			names.Values,
			"",
			"Path to a file for extra helm chart values",
		),
		cliflags.Bool(
			&cmd.ReuseValues,
			names.ReuseValues,
			true,
			"Reuse previous helm values on upgrade",
		),
		cliflags.String(
			&cmd.ChartPath,
			names.ChartPath,
			"",
			"The local chart path to deploy Devsy Pro",
		),
		cliflags.String(
			&cmd.ChartRepo,
			names.ChartRepo,
			"https://charts.devsy.sh/",
			"The chart repo to deploy Devsy Pro",
		),
	)
}

func (cmd *StartCmd) registerAuthFlags(startCmd *cobra.Command) {
	cliflags.Add(
		startCmd,
		cliflags.String(
			&cmd.Password,
			names.Password,
			"",
			"The password to use for the admin account. (If empty this will be the namespace UID)",
		),
		cliflags.String(&cmd.Email, names.Email, "", "The email to use for the installation"),
	)
}

func (cmd *StartCmd) registerLifecycleFlags(startCmd *cobra.Command) {
	cliflags.Add(
		startCmd,
		cliflags.Bool(
			&cmd.Upgrade,
			names.Upgrade,
			false,
			"If true, will try to upgrade the release",
		),
		cliflags.Bool(
			&cmd.Reset,
			names.Reset,
			false,
			"If true, an existing instance will be deleted before installing Devsy Pro",
		),
		cliflags.Bool(
			&cmd.NoWait,
			names.NoWait,
			false,
			"If true, will not wait after installing it",
		),
		cliflags.Bool(
			&cmd.NoTunnel,
			names.NoTunnel,
			false,
			"If true, will not create a loft.host tunnel for this installation",
		),
		cliflags.Bool(
			&cmd.NoLogin,
			names.NoLogin,
			false,
			"If true, will not login to a Devsy Pro instance on start",
		),
	)
}

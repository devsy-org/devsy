package cmdinternal

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agentcontainer"
	"github.com/devsy-org/devsy/cmd/internal/agentworkspace"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/spf13/cobra"
)

const agentJSONFormat = "json"

// NewAgentCmd is the hidden parent for commands that run inside a workspace or
// container, invoked by the daemon over the agent tunnel.
func NewAgentCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	agentCmd := &cobra.Command{
		Use:               "agent",
		Short:             "Devsy Agent",
		PersistentPreRunE: agentPreRunE(globalFlags),
		Hidden:            true,
	}

	agentCmd.AddCommand(agentworkspace.NewWorkspaceCmd(globalFlags))
	agentCmd.AddCommand(agentcontainer.NewContainerCmd(globalFlags))
	agentCmd.AddCommand(NewDaemonCmd(globalFlags))
	agentCmd.AddCommand(NewDaemonDiagnosticsCmd(globalFlags))
	agentCmd.AddCommand(NewContainerTunnelCmd(globalFlags))
	agentCmd.AddCommand(NewGitCredentialsCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureHelperCmd(globalFlags))
	agentCmd.AddCommand(NewDockerCredentialsCmd(globalFlags))
	return agentCmd
}

func agentPreRunE(globalFlags *flags.GlobalFlags) func(*cobra.Command, []string) error {
	return func(cobraCmd *cobra.Command, _ []string) error {
		root := cobraCmd
		for root.Parent() != nil {
			root = root.Parent()
		}
		if root.Annotations == nil {
			root.Annotations = map[string]string{}
		}
		root.Annotations[config.AgentExecutedAnnotation] = "true"
		if globalFlags.LogLevel == "" && root.PersistentFlags().Changed(names.LogLevel) {
			return fmt.Errorf("--%s cannot be empty", names.LogLevel)
		}
		if globalFlags.LogLevel != "" {
			if _, ok := log.LevelFromString(globalFlags.LogLevel); !ok {
				return fmt.Errorf(
					"invalid log level, expected one of: %s",
					strings.Join(log.ValidLevels(), ", "),
				)
			}
		}
		switch globalFlags.LogFormat {
		case "", "auto", "text", "plain", agentJSONFormat, "logfmt":
		default:
			return errors.New("invalid log format, expected auto, text, plain, json, or logfmt")
		}
		if _, err := output.ResolveMode(globalFlags.ResultFormat); err != nil {
			return err
		}

		if globalFlags.DevsyHome != "" {
			_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
		}
		envfile.Apply()
		log.Init(log.Config{
			Level:        globalFlags.LogLevel,
			DefaultLevel: log.LevelInfoName,
			Format:       agentJSONFormat,
			Redactor:     secrets.NewEnvironmentRedactor(os.Environ()),
		})

		return nil
	}
}

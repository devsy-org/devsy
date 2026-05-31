package pro

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	providercmd "github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"
)

// LogoutCmd holds the logout cmd flags.
type LogoutCmd struct {
	*proflags.GlobalFlags

	IgnoreNotFound bool
}

// NewLogoutCmd creates a new command.
func NewLogoutCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &LogoutCmd{
		GlobalFlags: flags,
	}
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of a Devsy Pro instance",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	logoutCmd.Flags().
		BoolVar(&cmd.IgnoreNotFound, "ignore-not-found", false, "Treat \"pro instance not found\" as a successful logout")
	return logoutCmd
}

//nolint:cyclop,funlen // logout sequences provider/daemon teardown; complexity reflects domain workflow
func (cmd *LogoutCmd) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("specify a pro instance to log out of")
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	// load pro instance config
	proInstanceName := args[0]
	proInstanceConfig, err := provider.LoadProInstanceConfig(
		devsyConfig.DefaultContext,
		proInstanceName,
	)
	if err != nil {
		if os.IsNotExist(err) && cmd.IgnoreNotFound {
			return nil
		}

		return fmt.Errorf("load pro instance %s: %w", proInstanceName, err)
	}

	providerConfig, err := provider.LoadProviderConfig(
		devsyConfig.DefaultContext,
		proInstanceConfig.Provider,
	)
	if err != nil {
		return fmt.Errorf("load provider: %w", err)
	}

	// stop daemon and clean up local workspaces
	if providerConfig.IsDaemonProvider() {
		// clean up local workspaces
		workspaces, err := workspace.ListLocalWorkspaces(
			devsyConfig.DefaultContext,
			false,
		)
		if err != nil {
			log.Warnf("Failed to list workspaces: %v", err)
		} else {
			cleanupLocalWorkspaces(
				ctx,
				devsyConfig,
				workspaces,
				providerConfig.Name,
				cmd.Owner,
			)
		}

		daemonClient := daemon.NewLocalClient(proInstanceConfig.Provider)
		err = daemonClient.Shutdown(ctx)
		if err != nil {
			log.Warnf("Failed to shut down daemon: %v", err)
		}
		log.Debug("Waiting for daemon to shut down")
		err = waitDaemonStopped(ctx, providerConfig.Name)
		if err != nil {
			log.Warnf("Failed to wait for daemon to be stopped: %v", err)
		}
	}

	// delete the provider config
	err = providercmd.DeleteProviderConfig(devsyConfig, proInstanceConfig.Provider, true)
	if err != nil {
		return err
	}

	// delete the pro instance dir itself
	proInstanceDir, err := provider.GetProInstanceDir(
		devsyConfig.DefaultContext,
		proInstanceConfig.Host,
	)
	if err != nil {
		return err
	}

	err = os.RemoveAll(proInstanceDir)
	if err != nil {
		return fmt.Errorf("delete pro instance dir: %w", err)
	}

	log.Infof("logged out of pro instance: proInstanceName=%s", proInstanceName)
	return nil
}

func cleanupLocalWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	workspaces []*provider.Workspace,
	providerName string,
	owner platform.OwnerFilter,
) {
	usedWorkspaces := []*provider.Workspace{}

	for _, workspace := range workspaces {
		if workspace.Provider.Name == providerName {
			usedWorkspaces = append(usedWorkspaces, workspace)
		}
	}

	if len(usedWorkspaces) > 0 {
		wg := sync.WaitGroup{}
		// try to force delete all workspaces in the background
		for _, w := range usedWorkspaces {
			wg.Add(1)
			go func(w provider.Workspace) {
				defer wg.Done()
				client, err := workspace.Get(ctx, workspace.GetOptions{
					DevsyConfig: devsyConfig,
					Args:        []string{w.ID},
					Owner:       owner,
					LocalOnly:   true,
				})
				if err != nil {
					log.Errorf("failed to get workspace: workspaceId=%s, err=%v", w.ID, err)
					return
				}
				// delete workspace folder
				err = clientimplementation.DeleteWorkspaceFolder(
					clientimplementation.DeleteWorkspaceFolderParams{
						Context:              devsyConfig.DefaultContext,
						WorkspaceID:          client.Workspace(),
						SSHConfigPath:        client.WorkspaceConfig().SSHConfigPath,
						SSHConfigIncludePath: client.WorkspaceConfig().SSHConfigIncludePath,
					},
				)
				if err != nil {
					log.Errorf("failed to remove workspace: workspaceId=%s, err=%v", w.ID, err)
					return
				}
				log.Infof("removed workspace: workspaceId=%s", w.ID)
			}(*w)
		}

		log.Infof("cleaning up local workspaces: count=%v", len(usedWorkspaces))
		wg.Wait()
	}
}

func waitDaemonStopped(ctx context.Context, providerName string) error {
	return wait.PollUntilContextTimeout(
		ctx,
		250*time.Millisecond,
		5*time.Second,
		true,
		func(ctx context.Context) (done bool, err error) {
			_, err = daemon.Dial(daemon.GetSocketAddr(providerName))
			if err != nil {
				return true, nil
			}

			return false, nil
		},
	)
}

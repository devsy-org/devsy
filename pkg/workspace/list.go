package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

const ProjectLabel = "devsy.sh/project"

func List(
	ctx context.Context,
	devsyConfig *config.Config,
	skipPro bool,
	owner platform.OwnerFilter,
) ([]*providerpkg.Workspace, error) {
	// list local workspaces
	localWorkspaces, err := ListLocalWorkspaces(devsyConfig.DefaultContext, skipPro)
	if err != nil {
		return nil, err
	}

	proWorkspaces := []*providerpkg.Workspace{}
	if !skipPro {
		// list remote workspaces
		proWorkspaceResults, err := listProWorkspaces(ctx, devsyConfig, owner)
		if err != nil {
			return nil, err
		}

		// extract pure workspace list first
		for _, result := range proWorkspaceResults {
			proWorkspaces = append(proWorkspaces, result.workspaces...)
		}

		// Check if every local file based workspace has a remote counterpart
		// If not, delete it
		// However, we need to differentiate between workspaces that are legitimately not available anymore
		// and the ones where we were temporarily not able to reach the host
		cleanedLocalWorkspaces := []*providerpkg.Workspace{}
		for _, localWorkspace := range localWorkspaces {
			if localWorkspace.IsPro() {
				if shouldDeleteLocalWorkspace(ctx, localWorkspace, proWorkspaceResults) {
					err = clientimplementation.DeleteWorkspaceFolder(
						clientimplementation.DeleteWorkspaceFolderParams{
							Context:              devsyConfig.DefaultContext,
							WorkspaceID:          localWorkspace.ID,
							SSHConfigPath:        localWorkspace.SSHConfigPath,
							SSHConfigIncludePath: localWorkspace.SSHConfigIncludePath,
						},
					)
					if err != nil {
						log.Debugf(
							"failed to delete local workspace %s: %v",
							localWorkspace.ID,
							err,
						)
					}
					continue
				}
			}

			cleanedLocalWorkspaces = append(cleanedLocalWorkspaces, localWorkspace)
		}
		localWorkspaces = cleanedLocalWorkspaces
	}

	// Set indexed by UID for deduplication
	workspaces := map[string]*providerpkg.Workspace{}

	// set local workspaces
	for _, workspace := range localWorkspaces {
		workspaces[workspace.UID] = workspace
	}

	// merge pro into local with pro taking precedence if UID matches
	for _, proWorkspace := range proWorkspaces {
		localWorkspace, ok := workspaces[proWorkspace.UID]
		if ok {
			// we want to use the local workspace IDE configuration
			proWorkspace.IDE = localWorkspace.IDE
		}

		workspaces[proWorkspace.UID] = proWorkspace
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, v := range workspaces {
		retWorkspaces = append(retWorkspaces, v)
	}

	return retWorkspaces, nil
}

func ListLocalWorkspaces(
	contextName string,
	skipPro bool,
) ([]*providerpkg.Workspace, error) {
	workspaceDir, err := providerpkg.GetWorkspacesDir(contextName)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(workspaceDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		workspaceConfig, err := providerpkg.LoadWorkspaceConfig(contextName, entry.Name())
		if err != nil {
			log.Warnf("could not load workspace: workspace=%s, error=%v", entry.Name(), err)
			continue
		}

		if skipPro && workspaceConfig.IsPro() {
			continue
		}

		retWorkspaces = append(retWorkspaces, workspaceConfig)
	}

	return retWorkspaces, nil
}

type listProWorkspacesResult struct {
	workspaces []*providerpkg.Workspace
	err        error
}

func listProWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	owner platform.OwnerFilter,
) (map[string]listProWorkspacesResult, error) {
	results := map[string]listProWorkspacesResult{}

	// lock around `results`
	var mu sync.Mutex
	wg := sync.WaitGroup{}

	for provider, providerContextConfig := range devsyConfig.Current().Providers {
		if !providerContextConfig.Initialized {
			continue
		}

		providerConfig, err := providerpkg.LoadProviderConfig(devsyConfig.DefaultContext, provider)
		if err != nil {
			log.Warnf("load provider config for provider: provider=%s, error=%v", provider, err)
			continue
		}

		// only get pro providers
		if !providerConfig.IsProxyProvider() && !providerConfig.IsDaemonProvider() {
			continue
		}

		wg.Go(func() {
			workspaces, err := listProWorkspacesForProvider(
				ctx,
				devsyConfig,
				provider,
				providerConfig,
				owner,
			)
			mu.Lock()
			defer mu.Unlock()
			results[provider] = listProWorkspacesResult{
				workspaces: workspaces,
				err:        err,
			}
		})
	}
	wg.Wait()

	return results, nil
}

func listProWorkspacesForProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider string,
	providerConfig *providerpkg.ProviderConfig,
	owner platform.OwnerFilter,
) ([]*providerpkg.Workspace, error) {
	var (
		instances []managementv1.DevsyWorkspaceInstance
		err       error
	)
	if providerConfig.IsProxyProvider() {
		instances, err = listInstancesProxyProvider(
			ctx,
			devsyConfig,
			provider,
			providerConfig,
		)
	} else if providerConfig.IsDaemonProvider() {
		instances, err = listInstancesDaemonProvider(ctx, provider, owner)
	} else {
		return nil, fmt.Errorf("cannot list pro workspaces with provider %s", provider)
	}
	if err != nil {
		if log.DebugEnabled() {
			log.Warnf("Failed to list pro workspaces for provider %s: %v", provider, err)
		}
		return nil, err
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, instance := range instances {
		if instance.GetLabels() == nil {
			log.Debugf("no labels for pro workspace \"%s\" found, skipping", instance.GetName())
			continue
		}

		// id
		id := instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel]
		if id == "" {
			log.Debugf("no ID label for pro workspace \"%s\" found, skipping", instance.GetName())
			continue
		}

		// uid
		uid := instance.GetLabels()[storagev1.DevsyWorkspaceUIDLabel]
		if uid == "" {
			log.Debugf("no UID label for pro workspace \"%s\" found, skipping", instance.GetName())
			continue
		}

		// project
		projectName := instance.GetLabels()[ProjectLabel]

		// source
		source := providerpkg.WorkspaceSource{}
		if instance.Annotations != nil &&
			instance.Annotations[storagev1.DevsyWorkspaceSourceAnnotation] != "" {
			// source to workspace config source
			rawSource := instance.Annotations[storagev1.DevsyWorkspaceSourceAnnotation]
			s := providerpkg.ParseWorkspaceSource(rawSource)
			if s == nil {
				log.Warnf("unable to parse workspace source: source=%s", rawSource)
			} else {
				source = *s
			}
		}

		// last used timestamp
		var lastUsedTimestamp types.Time
		sleepModeConfig := instance.Status.SleepModeConfig
		if sleepModeConfig != nil {
			lastUsedTimestamp = types.Unix(sleepModeConfig.Status.LastActivity, 0)
		} else {
			var ts int64
			if instance.Annotations != nil {
				if val, ok := instance.Annotations["sleepmode.devsy.sh/last-activity"]; ok {
					var err error
					if ts, err = strconv.ParseInt(val, 10, 64); err != nil {
						log.Warn(
							"received invalid sleepmode.devsy.sh/last-activity from ",
							instance.GetName(),
						)
					}
				}
			}
			lastUsedTimestamp = types.Unix(ts, 0)
		}

		// creation timestamp
		creationTimestamp := types.Time{}
		if !instance.CreationTimestamp.IsZero() {
			creationTimestamp = types.NewTime(instance.CreationTimestamp.Time)
		}

		workspace := providerpkg.Workspace{
			ID:      id,
			UID:     uid,
			Context: devsyConfig.DefaultContext,
			Source:  source,
			Provider: providerpkg.WorkspaceProviderConfig{
				Name: provider,
			},
			LastUsedTimestamp: lastUsedTimestamp,
			CreationTimestamp: creationTimestamp,
			Pro: &providerpkg.ProMetadata{
				InstanceName: instance.GetName(),
				Project:      projectName,
				DisplayName:  instance.Spec.DisplayName,
			},
		}
		retWorkspaces = append(retWorkspaces, &workspace)
	}

	return retWorkspaces, nil
}

func shouldDeleteLocalWorkspace(
	ctx context.Context,
	localWorkspace *providerpkg.Workspace,
	proWorkspaceResults map[string]listProWorkspacesResult,
) bool {
	// get the correct result for this local workspace
	res, ok := proWorkspaceResults[localWorkspace.Provider.Name]
	if !ok {
		return false
	}
	// Don't delete the workspace if we encountered any error fetching the remote workspaces.
	// This could potentially be destructive so we err or the side of caution and only allow
	// deletion if fetching the remote workspace was successful
	if res.err != nil {
		return false
	}

	if localWorkspace.Imported {
		// does remote still exist?
		if ok := checkInstanceExists(ctx, localWorkspace); ok {
			return false
		}
	}

	hasProCounterpart := slices.ContainsFunc(res.workspaces, func(w *providerpkg.Workspace) bool {
		return localWorkspace.UID == w.UID
	})
	return !hasProCounterpart
}

func listInstancesProxyProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider string,
	providerConfig *providerpkg.ProviderConfig,
) ([]managementv1.DevsyWorkspaceInstance, error) {
	opts := devsyConfig.ProviderOptions(provider)
	opts[config.EnvLoftFilterByOwner] = config.OptionValue{Value: "true"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "listWorkspaces",
		Command: providerConfig.Exec.Proxy.List.Workspaces,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  providerConfig,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}); err != nil {
		return nil, fmt.Errorf("failed to list pro workspaces: %s: %w", stderr.String(), err)
	}
	if stdout.Len() == 0 {
		return nil, nil
	}

	instances := []managementv1.DevsyWorkspaceInstance{}
	if err := json.Unmarshal(stdout.Bytes(), &instances); err != nil {
		return nil, err
	}

	return instances, nil
}

func listInstancesDaemonProvider(
	ctx context.Context,
	provider string,
	owner platform.OwnerFilter,
) ([]managementv1.DevsyWorkspaceInstance, error) {
	return daemon.NewLocalClient(provider).ListWorkspaces(ctx, owner)
}

func checkInstanceExists(ctx context.Context, workspace *providerpkg.Workspace) bool {
	instance, err := daemon.NewLocalClient(workspace.Provider.Name).GetWorkspace(ctx, workspace.UID)
	if err != nil || instance == nil {
		return false
	}

	return true
}

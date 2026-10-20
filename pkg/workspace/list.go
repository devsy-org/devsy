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

func List(
	ctx context.Context,
	devsyConfig *config.Config,
	skipPlatform bool,
	owner platform.OwnerFilter,
) ([]*providerpkg.Workspace, error) {
	// list local workspaces
	localWorkspaces, err := ListLocalWorkspaces(devsyConfig.DefaultContext, skipPlatform)
	if err != nil {
		return nil, err
	}

	platformWorkspaces := []*providerpkg.Workspace{}
	if !skipPlatform {
		platformWorkspaces, localWorkspaces = reconcilePlatformWorkspaces(
			ctx, devsyConfig, localWorkspaces, owner,
		)
	}

	return mergeWorkspaces(localWorkspaces, platformWorkspaces), nil
}

func reconcilePlatformWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	localWorkspaces []*providerpkg.Workspace,
	owner platform.OwnerFilter,
) ([]*providerpkg.Workspace, []*providerpkg.Workspace) {
	platformWorkspaceResults := listPlatformWorkspaces(ctx, devsyConfig, owner)

	platformWorkspaces := []*providerpkg.Workspace{}
	for _, result := range platformWorkspaceResults {
		platformWorkspaces = append(platformWorkspaces, result.workspaces...)
	}

	// Check if every local file based workspace has a remote counterpart.
	// If not, delete it, while differentiating between workspaces that are
	// legitimately gone and those where the host was temporarily unreachable.
	cleanedLocalWorkspaces := []*providerpkg.Workspace{}
	for _, localWorkspace := range localWorkspaces {
		if localWorkspace.IsPlatform() &&
			shouldDeleteLocalWorkspace(ctx, localWorkspace, platformWorkspaceResults) {
			deleteLocalWorkspace(devsyConfig, localWorkspace)
			continue
		}
		cleanedLocalWorkspaces = append(cleanedLocalWorkspaces, localWorkspace)
	}

	return platformWorkspaces, cleanedLocalWorkspaces
}

func deleteLocalWorkspace(devsyConfig *config.Config, localWorkspace *providerpkg.Workspace) {
	err := clientimplementation.DeleteWorkspaceFolder(
		clientimplementation.DeleteWorkspaceFolderParams{
			Context:              devsyConfig.DefaultContext,
			WorkspaceID:          localWorkspace.ID,
			SSHConfigPath:        localWorkspace.SSHConfigPath,
			SSHConfigIncludePath: localWorkspace.SSHConfigIncludePath,
		},
	)
	if err != nil {
		log.Debugf("failed to delete local workspace %s: %v", localWorkspace.ID, err)
	}
}

func mergeWorkspaces(
	localWorkspaces, platformWorkspaces []*providerpkg.Workspace,
) []*providerpkg.Workspace {
	workspaces := map[string]*providerpkg.Workspace{}
	for _, workspace := range localWorkspaces {
		workspaces[workspace.UID] = workspace
	}

	for _, platformWorkspace := range platformWorkspaces {
		if localWorkspace, ok := workspaces[platformWorkspace.UID]; ok {
			platformWorkspace.IDE = localWorkspace.IDE
		}
		workspaces[platformWorkspace.UID] = platformWorkspace
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, v := range workspaces {
		retWorkspaces = append(retWorkspaces, v)
	}

	return retWorkspaces
}

func ListLocalWorkspaces(
	contextName string,
	skipPlatform bool,
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
		if workspaceConfig := loadLocalWorkspaceEntry(
			contextName,
			entry.Name(),
			skipPlatform,
		); workspaceConfig != nil {
			retWorkspaces = append(retWorkspaces, workspaceConfig)
		}
	}

	return retWorkspaces, nil
}

func loadLocalWorkspaceEntry(
	contextName, name string,
	skipPlatform bool,
) *providerpkg.Workspace {
	if strings.HasPrefix(name, ".") {
		return nil
	}

	workspaceConfig, err := providerpkg.LoadWorkspaceConfig(contextName, name)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("skipping workspace without config: workspace=%s", name)
		} else {
			log.Warnf("could not load workspace: workspace=%s, error=%v", name, err)
		}
		return nil
	}

	if skipPlatform && workspaceConfig.IsPlatform() {
		return nil
	}

	return workspaceConfig
}

func CountLocalWorkspaces(contextName string) (int, error) {
	workspaces, err := ListLocalWorkspaces(contextName, false)
	if err != nil {
		return 0, err
	}
	return len(workspaces), nil
}

type listPlatformWorkspacesResult struct {
	workspaces []*providerpkg.Workspace
	err        error
}

func listPlatformWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	owner platform.OwnerFilter,
) map[string]listPlatformWorkspacesResult {
	results := map[string]listPlatformWorkspacesResult{}

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

		// only get platform providers
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
			results[provider] = listPlatformWorkspacesResult{
				workspaces: workspaces,
				err:        err,
			}
		})
	}

	wg.Wait()
	return results
}

func listProWorkspacesForProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider string,
	providerConfig *providerpkg.ProviderConfig,
	owner platform.OwnerFilter,
) ([]*providerpkg.Workspace, error) {
	instances, err := listPlatformInstances(ctx, listPlatformInstancesParams{
		devsyConfig:    devsyConfig,
		provider:       provider,
		providerConfig: providerConfig,
		owner:          owner,
	})
	if err != nil {
		if log.DebugEnabled() {
			log.Warnf("Failed to list platform workspaces for provider %s: %v", provider, err)
		}
		return nil, err
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, instance := range instances {
		if workspace := platformWorkspaceFromInstance(
			instance,
			provider,
			devsyConfig.DefaultContext,
		); workspace != nil {
			retWorkspaces = append(retWorkspaces, workspace)
		}
	}

	return retWorkspaces, nil
}

type listPlatformInstancesParams struct {
	devsyConfig    *config.Config
	provider       string
	providerConfig *providerpkg.ProviderConfig
	owner          platform.OwnerFilter
}

func listPlatformInstances(
	ctx context.Context,
	params listPlatformInstancesParams,
) ([]managementv1.DevsyWorkspaceInstance, error) {
	switch {
	case params.providerConfig.IsProxyProvider():
		return listInstancesProxyProvider(
			ctx,
			params.devsyConfig,
			params.provider,
			params.providerConfig,
		)
	case params.providerConfig.IsDaemonProvider():
		return listInstancesDaemonProvider(ctx, params.provider, params.owner)
	default:
		return nil, fmt.Errorf("cannot list platform workspaces with provider %s", params.provider)
	}
}

func platformWorkspaceFromInstance(
	instance managementv1.DevsyWorkspaceInstance,
	provider string,
	defaultContext string,
) *providerpkg.Workspace {
	if instance.GetLabels() == nil {
		log.Debugf("no labels for platform workspace %q found, skipping", instance.GetName())
		return nil
	}

	id := instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel]
	if id == "" {
		log.Debugf("no ID label for platform workspace %q found, skipping", instance.GetName())
		return nil
	}

	uid := instance.GetLabels()[storagev1.DevsyWorkspaceUIDLabel]
	if uid == "" {
		log.Debugf("no UID label for platform workspace %q found, skipping", instance.GetName())
		return nil
	}

	return &providerpkg.Workspace{
		ID:      id,
		UID:     uid,
		Context: defaultContext,
		Source:  platformWorkspaceSource(instance),
		Provider: providerpkg.WorkspaceProviderConfig{
			Name: provider,
		},
		LastUsedTimestamp: platformWorkspaceLastUsed(instance),
		CreationTimestamp: platformWorkspaceCreationTimestamp(instance),
		Platform: &providerpkg.PlatformMetadata{
			InstanceName: instance.GetName(),
			Project:      instance.GetLabels()[config.K8sProjectLabel],
			DisplayName:  instance.Spec.DisplayName,
		},
	}
}

func platformWorkspaceSource(
	instance managementv1.DevsyWorkspaceInstance,
) providerpkg.WorkspaceSource {
	source := providerpkg.WorkspaceSource{}
	if instance.Annotations == nil ||
		instance.Annotations[storagev1.DevsyWorkspaceSourceAnnotation] == "" {
		return source
	}

	rawSource := instance.Annotations[storagev1.DevsyWorkspaceSourceAnnotation]
	s := providerpkg.ParseWorkspaceSource(rawSource)
	if s == nil {
		log.Warnf("unable to parse workspace source: source=%s", rawSource)
		return source
	}
	return *s
}

func platformWorkspaceLastUsed(instance managementv1.DevsyWorkspaceInstance) types.Time {
	if sleepModeConfig := instance.Status.SleepModeConfig; sleepModeConfig != nil {
		return types.Unix(sleepModeConfig.Status.LastActivity, 0)
	}

	var ts int64
	if instance.Annotations != nil {
		if val, ok := instance.Annotations["sleepmode.devsy.sh/last-activity"]; ok {
			if parsed, err := strconv.ParseInt(val, 10, 64); err != nil {
				log.Warn(
					"received invalid sleepmode.devsy.sh/last-activity from ",
					instance.GetName(),
				)
			} else {
				ts = parsed
			}
		}
	}
	return types.Unix(ts, 0)
}

func platformWorkspaceCreationTimestamp(instance managementv1.DevsyWorkspaceInstance) types.Time {
	if instance.CreationTimestamp.IsZero() {
		return types.Time{}
	}
	return types.NewTime(instance.CreationTimestamp.Time)
}

func shouldDeleteLocalWorkspace(
	ctx context.Context,
	localWorkspace *providerpkg.Workspace,
	platformWorkspaceResults map[string]listPlatformWorkspacesResult,
) bool {
	// get the correct result for this local workspace
	res, ok := platformWorkspaceResults[localWorkspace.Provider.Name]
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

	hasPlatformCounterpart := slices.ContainsFunc(
		res.workspaces,
		func(w *providerpkg.Workspace) bool {
			return localWorkspace.UID == w.UID
		},
	)
	return !hasPlatformCounterpart
}

func listInstancesProxyProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	provider string,
	providerConfig *providerpkg.ProviderConfig,
) ([]managementv1.DevsyWorkspaceInstance, error) {
	opts := devsyConfig.ProviderOptions(provider)
	opts[config.EnvFilterByOwner] = config.OptionValue{Value: "true"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := clientimplementation.RunCommandWithBinaries(
		ctx,
		clientimplementation.WorkspaceCommandConfig{
			Command:              providerConfig.Exec.Proxy.List.Workspaces,
			WorkspaceContextName: devsyConfig.DefaultContext,
			Options:              opts,
			Config:               providerConfig,
			Stdout:               &stdout,
			Stderr:               &stderr,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to list platform workspaces: %s: %w", stderr.String(), err)
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

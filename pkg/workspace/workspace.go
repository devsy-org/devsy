package workspace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation/daemonclient"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/file"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/terminal"
	"github.com/devsy-org/devsy/pkg/types"
)

const maxWorkspaceIDLength = 48

var errProvideWorkspaceArg = errors.New(
	"provide a workspace source: a folder, git repository or image",
)

// ErrWorkspaceNotFound maps to the Retryable exit code, which the backhaul SSH
// command retries on.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// RemoteCreator is implemented by clients (ProxyClient, DaemonClient) that
// create workspaces on a remote platform.
type RemoteCreator interface {
	Create(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error
}

// ResolveParams carries the `devsy up|build` CLI input used to find or create a workspace.
type ResolveParams struct {
	IDE                  string
	IDEOptions           []string
	Args                 []string
	DesiredID            string
	DesiredMachine       string
	ProviderUserOptions  []string
	ReconfigureProvider  bool
	DevContainerImage    string
	DevContainerPath     string
	DevContainerSource   string
	SSHConfigPath        string
	SSHConfigIncludePath string
	Source               *providerpkg.WorkspaceSource
	UID                  string
	ChangeLastUsed       bool
	Owner                platform.OwnerFilter
}

// Resolve finds an existing workspace matching the params or creates a new one,
// returning a ready-to-use client.
func Resolve(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
) (client.BaseWorkspaceClient, error) {
	if err := validateDesiredID(params.DesiredID); err != nil {
		return nil, err
	}

	resolved, err := resolveWorkspace(ctx, devsyConfig, params)
	if err != nil {
		return nil, err
	}

	workspace, err := ideparse.RefreshIDEOptions(
		devsyConfig,
		resolved.workspace,
		params.IDE,
		params.IDEOptions,
	)
	if err != nil {
		return nil, err
	}

	if err := applyDevContainerOverrides(workspace, params); err != nil {
		return nil, err
	}

	workspaceClient, err := getWorkspaceClient(
		devsyConfig,
		resolved.provider,
		workspace,
		resolved.machine,
	)
	if err != nil {
		return nil, err
	}

	if err := workspaceClient.RefreshOptions(
		ctx,
		params.ProviderUserOptions,
		params.ReconfigureProvider,
	); err != nil {
		return nil, err
	}

	return workspaceClient, nil
}

func validateDesiredID(desiredID string) error {
	if desiredID == "" {
		return nil
	}
	if providerpkg.ProviderNameRegEx.MatchString(desiredID) {
		return errors.New("workspace name may only contain lowercase letters, numbers or dashes")
	}
	if len(desiredID) > maxWorkspaceIDLength {
		return fmt.Errorf("workspace name cannot exceed %d characters", maxWorkspaceIDLength)
	}
	return nil
}

func applyDevContainerOverrides(workspace *providerpkg.Workspace, params ResolveParams) error {
	changed := applyDevContainerFields(workspace, params)

	if !changed && workspace.Source.Container == "" {
		return nil
	}

	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}

	return nil
}

func applyDevContainerFields(workspace *providerpkg.Workspace, params ResolveParams) bool {
	changed := false
	if params.DevContainerImage != "" && workspace.DevContainerImage != params.DevContainerImage {
		workspace.DevContainerImage = params.DevContainerImage
		changed = true
	}
	if params.DevContainerPath != "" && workspace.DevContainerPath != params.DevContainerPath {
		workspace.DevContainerPath = params.DevContainerPath
		changed = true
	}
	if params.DevContainerSource != "" &&
		workspace.DevContainerSource != params.DevContainerSource {
		workspace.DevContainerSource = params.DevContainerSource
		changed = true
	}
	return changed
}

func getWorkspaceClient(
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
	workspace *providerpkg.Workspace,
	machine *providerpkg.Machine,
) (client.BaseWorkspaceClient, error) {
	switch {
	case provider.IsProxyProvider():
		return clientimplementation.NewProxyClient(devsyConfig, provider, workspace)
	case provider.IsDaemonProvider():
		return daemonclient.New(devsyConfig, provider, workspace)
	default:
		return clientimplementation.NewWorkspaceClient(devsyConfig, provider, workspace, machine)
	}
}

// GetOptions holds the parameters for retrieving an existing workspace.
type GetOptions struct {
	DevsyConfig    *config.Config
	Args           []string
	ChangeLastUsed bool
	Owner          platform.OwnerFilter
	LocalOnly      bool
}

// Get retrieves an existing workspace, prompting for a selection when no args
// are given.
func Get(ctx context.Context, opts GetOptions) (client.BaseWorkspaceClient, error) {
	if len(opts.Args) == 0 {
		resolved, err := selectWorkspace(
			ctx,
			opts.DevsyConfig,
			selectWorkspaceParams{
				changeLastUsed: opts.ChangeLastUsed,
				owner:          opts.Owner,
				localOnly:      opts.LocalOnly,
			},
		)
		if err != nil {
			return nil, err
		}

		return getWorkspaceClient(
			opts.DevsyConfig,
			resolved.provider,
			resolved.workspace,
			resolved.machine,
		)
	}

	workspace, err := findWorkspaceByArgs(ctx, opts)
	if err != nil {
		return nil, err
	}
	if workspace == nil {
		return nil, fmt.Errorf("%w for args: %v", ErrWorkspaceNotFound, opts.Args)
	}

	resolved, err := loadExistingWorkspace(
		opts.DevsyConfig,
		workspace.ID,
		opts.ChangeLastUsed,
	)
	if err != nil {
		return nil, err
	}

	return getWorkspaceClient(
		opts.DevsyConfig,
		resolved.provider,
		resolved.workspace,
		resolved.machine,
	)
}

func findWorkspaceByArgs(ctx context.Context, opts GetOptions) (*providerpkg.Workspace, error) {
	if opts.LocalOnly {
		return findLocalWorkspace(opts.DevsyConfig, opts.Args, ""), nil
	}
	return findWorkspace(ctx, opts.DevsyConfig, opts.Args, "", opts.Owner)
}

// Exists returns the ID of a matching workspace, or an empty string if none
// exists.
func Exists(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
	owner platform.OwnerFilter,
) string {
	workspace, _ := findWorkspace(ctx, devsyConfig, args, workspaceID, owner)
	if workspace == nil {
		return ""
	}

	return workspace.ID
}

// resolveWorkspace finds an existing workspace matching the params or creates a
// new one, removing the freshly-created folder if creation fails.
func resolveWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
) (resolvedWorkspace, error) {
	if len(params.Args) == 0 {
		return resolveWorkspaceWithoutArgs(ctx, devsyConfig, params)
	}

	isLocalPath, name := file.IsLocalDir(params.Args[0])
	workspaceID := ToID(name)

	if params.DesiredID != "" {
		if Exists(ctx, devsyConfig, nil, params.DesiredID, params.Owner) != "" {
			log.Debugf("workspace id already exists: desiredID=%s", params.DesiredID)
			return loadExistingWorkspace(devsyConfig, params.DesiredID, params.ChangeLastUsed)
		}
		workspaceID = params.DesiredID
	} else if Exists(ctx, devsyConfig, nil, workspaceID, params.Owner) != "" {
		log.Debugf("workspace already exists: workspaceID=%s", workspaceID)
		return loadExistingWorkspace(devsyConfig, workspaceID, params.ChangeLastUsed)
	}

	resolved, err := createWorkspace(ctx, devsyConfig, createWorkspaceParams{
		workspaceID:          workspaceID,
		name:                 name,
		desiredMachine:       params.DesiredMachine,
		providerUserOptions:  params.ProviderUserOptions,
		sshConfigPath:        params.SSHConfigPath,
		sshConfigIncludePath: params.SSHConfigIncludePath,
		source:               params.Source,
		isLocalPath:          isLocalPath,
		uid:                  params.UID,
	})
	if err != nil {
		_ = clientimplementation.DeleteWorkspaceFolder(
			clientimplementation.DeleteWorkspaceFolderParams{
				Context:              devsyConfig.DefaultContext,
				WorkspaceID:          workspaceID,
				SSHConfigPath:        params.SSHConfigPath,
				SSHConfigIncludePath: params.SSHConfigIncludePath,
			},
		)
		return resolvedWorkspace{}, err
	}

	return resolved, nil
}

type resolvedWorkspace struct {
	provider  *providerpkg.ProviderConfig
	workspace *providerpkg.Workspace
	machine   *providerpkg.Machine
}

func resolveWorkspaceWithoutArgs(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
) (resolvedWorkspace, error) {
	if params.DesiredID != "" {
		workspace, err := findWorkspace(ctx, devsyConfig, nil, params.DesiredID, params.Owner)
		if err != nil {
			return resolvedWorkspace{}, fmt.Errorf("find workspace: %w", err)
		}
		if workspace == nil {
			return resolvedWorkspace{}, fmt.Errorf("workspace %s doesn't exist", params.DesiredID)
		}
		return loadExistingWorkspace(devsyConfig, workspace.ID, params.ChangeLastUsed)
	}

	return selectWorkspace(ctx, devsyConfig, selectWorkspaceParams{
		changeLastUsed:       params.ChangeLastUsed,
		sshConfigPath:        params.SSHConfigPath,
		sshConfigIncludePath: params.SSHConfigIncludePath,
		owner:                params.Owner,
	})
}

type createWorkspaceParams struct {
	workspaceID          string
	name                 string
	desiredMachine       string
	providerUserOptions  []string
	sshConfigPath        string
	sshConfigIncludePath string
	source               *providerpkg.WorkspaceSource
	isLocalPath          bool
	uid                  string
}

// createWorkspace builds a new workspace config and provisions any backing
// machine or remote instance the provider requires.
func createWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params createWorkspaceParams,
) (resolvedWorkspace, error) {
	provider, err := loadInitializedProvider(devsyConfig)
	if err != nil {
		return resolvedWorkspace{}, err
	}

	workspace := resolveWorkspaceConfig(ctx, provider, devsyConfig, resolveWorkspaceConfigParams{
		name:                 params.name,
		workspaceID:          params.workspaceID,
		source:               params.source,
		isLocalPath:          params.isLocalPath,
		sshConfigPath:        params.sshConfigPath,
		sshConfigIncludePath: params.sshConfigIncludePath,
		uid:                  params.uid,
	})

	if err := assignDesiredMachine(provider, workspace, params.desiredMachine); err != nil {
		return resolvedWorkspace{}, err
	}

	workspace, machineConfig, err := provisionWorkspaceBacking(ctx, machineProvisionParams{
		devsyConfig:         devsyConfig,
		provider:            provider,
		workspace:           workspace,
		providerUserOptions: params.providerUserOptions,
	})
	if err != nil {
		return resolvedWorkspace{}, err
	}

	return resolvedWorkspace{
		provider:  provider.Config,
		workspace: workspace,
		machine:   machineConfig,
	}, nil
}

func provisionWorkspaceBacking(
	ctx context.Context,
	p machineProvisionParams,
) (*providerpkg.Workspace, *providerpkg.Machine, error) {
	switch {
	case p.provider.Config.IsMachineProvider() && p.workspace.Machine.ID == "":
		machineConfig, err := provisionManagedMachine(ctx, p)
		return p.workspace, machineConfig, err
	case p.provider.Config.IsProxyProvider() || p.provider.Config.IsDaemonProvider():
		updated, err := resolveProWorkspace(ctx, p.devsyConfig, p.provider, p.workspace)
		return updated, nil, err
	default:
		machineConfig, err := saveAndLoadExistingMachine(p.provider, p.workspace)
		return p.workspace, machineConfig, err
	}
}

func loadInitializedProvider(devsyConfig *config.Config) (*ProviderWithOptions, error) {
	provider, _, err := LoadProviders(devsyConfig)
	if err != nil {
		return nil, err
	}
	if provider.State == nil || !provider.State.Initialized {
		return nil, fmt.Errorf(
			"provider %q is not initialized, run 'devsy provider init %s' first",
			provider.Config.Name,
			provider.Config.Name,
		)
	}
	return provider, nil
}

func assignDesiredMachine(
	provider *ProviderWithOptions,
	workspace *providerpkg.Workspace,
	desiredMachine string,
) error {
	if desiredMachine == "" {
		return nil
	}
	if !provider.Config.IsMachineProvider() {
		return fmt.Errorf("provider %s cannot create servers", provider.Config.Name)
	}
	if !providerpkg.MachineExists(workspace.Context, desiredMachine) {
		return fmt.Errorf("server %s doesn't exist", desiredMachine)
	}

	workspace.Machine = providerpkg.WorkspaceMachineConfig{ID: desiredMachine}
	return nil
}

type machineProvisionParams struct {
	devsyConfig         *config.Config
	provider            *ProviderWithOptions
	workspace           *providerpkg.Workspace
	providerUserOptions []string
}

func provisionManagedMachine(
	ctx context.Context,
	params machineProvisionParams,
) (*providerpkg.Machine, error) {
	workspace := params.workspace
	if params.provider.State != nil && params.provider.State.SingleMachine {
		workspace.Machine.ID = SingleMachineName(params.devsyConfig, params.provider.Config.Name)
	} else {
		workspace.Machine.ID = encoding.CreateNewUIDShort(workspace.ID)
		workspace.Machine.AutoDelete = true
	}

	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	if providerpkg.MachineExists(params.devsyConfig.DefaultContext, workspace.Machine.ID) {
		log.Infof("reuse existing machine %q for workspace %q", workspace.Machine.ID, workspace.ID)
		machineConfig, err := providerpkg.LoadMachineConfig(workspace.Context, workspace.Machine.ID)
		if err != nil {
			return nil, fmt.Errorf("load machine config: %w", err)
		}
		return machineConfig, nil
	}

	return createManagedMachine(ctx, params)
}

// createManagedMachine creates the machine folder and drives the machine client
// through option refresh and creation, removing the folder on failure.
func createManagedMachine(
	ctx context.Context,
	params machineProvisionParams,
) (*providerpkg.Machine, error) {
	workspace := params.workspace
	machineConfig, err := createMachine(
		workspace.Context,
		workspace.Machine.ID,
		params.provider.Config.Name,
	)
	if err != nil {
		return nil, err
	}

	machineClient, err := clientimplementation.NewMachineClient(
		params.devsyConfig,
		params.provider.Config,
		machineConfig,
	)
	if err != nil {
		return nil, cleanupMachineFolder(machineConfig, err)
	}

	if err := machineClient.RefreshOptions(ctx, params.providerUserOptions, false); err != nil {
		return nil, cleanupMachineFolder(machineConfig, err)
	}

	if err := machineClient.Create(ctx); err != nil {
		return nil, cleanupMachineFolder(machineConfig, err)
	}

	return machineConfig, nil
}

// cleanupMachineFolder removes a machine folder and returns the original cause.
func cleanupMachineFolder(machineConfig *providerpkg.Machine, cause error) error {
	_ = clientimplementation.DeleteMachineFolder(machineConfig.Context, machineConfig.ID)
	return cause
}

// resolveProWorkspace saves the workspace, hands provisioning to the remote pro
// process, and reloads the config it wrote back. The pro process renders its
// form over os i/o and cannot call back into this process, so state is exchanged
// through the workspace config file.
func resolveProWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *ProviderWithOptions,
	workspace *providerpkg.Workspace,
) (*providerpkg.Workspace, error) {
	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	err := resolveProInstance(proInstanceParams{
		ctx:          ctx,
		devsyConfig:  devsyConfig,
		providerName: provider.Config.Name,
		workspace:    workspace,
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
	})
	if err != nil {
		return nil, err
	}

	reloaded, err := providerpkg.LoadWorkspaceConfig(workspace.Context, workspace.ID)
	if err != nil {
		return nil, err
	}

	return reloaded, nil
}

func saveAndLoadExistingMachine(
	provider *ProviderWithOptions,
	workspace *providerpkg.Workspace,
) (*providerpkg.Machine, error) {
	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	if provider.Config.IsMachineProvider() && workspace.Machine.ID != "" {
		machineConfig, err := providerpkg.LoadMachineConfig(workspace.Context, workspace.Machine.ID)
		if err != nil {
			return nil, fmt.Errorf("load machine config: %w", err)
		}
		return machineConfig, nil
	}

	return nil, nil
}

type resolveWorkspaceConfigParams struct {
	name                 string
	workspaceID          string
	source               *providerpkg.WorkspaceSource
	isLocalPath          bool
	sshConfigPath        string
	sshConfigIncludePath string
	uid                  string
}

func resolveWorkspaceConfig(
	ctx context.Context,
	defaultProvider *ProviderWithOptions,
	devsyConfig *config.Config,
	params resolveWorkspaceConfigParams,
) *providerpkg.Workspace {
	now := types.Now()
	uid := params.uid
	if uid == "" {
		uid = encoding.CreateNewUID(devsyConfig.DefaultContext, params.workspaceID)
	}

	workspace := &providerpkg.Workspace{
		ID:      params.workspaceID,
		UID:     uid,
		Context: devsyConfig.DefaultContext,
		Provider: providerpkg.WorkspaceProviderConfig{
			Name: defaultProvider.Config.Name,
		},
		CreationTimestamp:    now,
		LastUsedTimestamp:    now,
		SSHConfigPath:        params.sshConfigPath,
		SSHConfigIncludePath: params.sshConfigIncludePath,
	}

	workspace.Source, workspace.Picture = resolveWorkspaceSource(ctx, params)

	return workspace
}

// resolveWorkspaceSource classifies the workspace source, probing git and image
// registries as needed. It returns the source and, for git repositories, a
// project picture.
func resolveWorkspaceSource(
	ctx context.Context,
	params resolveWorkspaceConfigParams,
) (providerpkg.WorkspaceSource, string) {
	if params.source != nil {
		return *params.source, ""
	}

	if params.isLocalPath {
		return providerpkg.WorkspaceSource{LocalFolder: params.name}, ""
	}

	info := git.NormalizeRepository(params.name)
	if strings.HasSuffix(params.name, ".git") ||
		git.PingRepository(info.Repository, git.GetDefaultExtraEnv(false)) {
		return gitWorkspaceSource(info, info.Repository), getProjectImage(params.name)
	}

	if _, err := image.GetImage(ctx, params.name); err == nil {
		return providerpkg.WorkspaceSource{Image: params.name}, ""
	}

	return gitWorkspaceSource(info, cmp.Or(info.Repository, params.name)), ""
}

// gitWorkspaceSource builds a git workspace source from parsed repository info,
// using repository for the GitRepository field.
func gitWorkspaceSource(info *git.GitInfo, repository string) providerpkg.WorkspaceSource {
	return providerpkg.WorkspaceSource{
		GitRepository:  repository,
		GitPRReference: info.PR,
		GitBranch:      info.Branch,
		GitCommit:      info.Commit,
		GitSubPath:     info.SubPath,
	}
}

// ensureWorkspaceID returns the workspace id, deriving it from the first arg
// when not explicitly provided, or an empty string when neither is available.
func ensureWorkspaceID(args []string, workspaceID string) string {
	if len(args) == 0 && workspaceID == "" {
		return ""
	}

	if workspaceID == "" {
		_, name := file.IsLocalDir(args[0])
		workspaceID = ToID(name)
	}

	return workspaceID
}

func findLocalWorkspace(
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
) *providerpkg.Workspace {
	workspaceID = ensureWorkspaceID(args, workspaceID)
	if workspaceID == "" {
		return nil
	}

	allWorkspaces, err := ListLocalWorkspaces(devsyConfig.DefaultContext, false)
	if err != nil {
		log.Debugf("list workspaces: %v", err)
		return nil
	}

	for _, workspace := range allWorkspaces {
		if workspace.ID == workspaceID {
			return workspace
		}
	}

	return nil
}

// findWorkspace looks up a workspace among all known ones, marking a matching
// pro workspace as imported and persisting it locally.
func findWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
	owner platform.OwnerFilter,
) (*providerpkg.Workspace, error) {
	workspaceID = ensureWorkspaceID(args, workspaceID)
	if workspaceID == "" {
		return nil, nil
	}

	allWorkspaces, err := List(ctx, devsyConfig, false, owner)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	for _, workspace := range allWorkspaces {
		if workspace.ID != workspaceID {
			continue
		}

		if workspace.IsPro() {
			workspace.Imported = true
			if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
				return nil, fmt.Errorf("save workspace config: %w", err)
			}
		}

		return workspace, nil
	}

	return nil, nil
}

type selectWorkspaceParams struct {
	changeLastUsed       bool
	sshConfigPath        string
	sshConfigIncludePath string
	owner                platform.OwnerFilter
	localOnly            bool
}

func selectWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params selectWorkspaceParams,
) (resolvedWorkspace, error) {
	if !terminal.IsTerminalIn {
		return resolvedWorkspace{}, errProvideWorkspaceArg
	}

	workspaces, err := listSelectableWorkspaces(ctx, devsyConfig, params)
	if err != nil {
		return resolvedWorkspace{}, err
	}
	if len(workspaces) == 0 {
		return resolvedWorkspace{}, errors.Join(ErrNoWorkspaceFound, errProvideWorkspaceArg)
	}

	selectedWorkspace, err := promptWorkspaceSelection(workspaces)
	if err != nil {
		return resolvedWorkspace{}, err
	}

	sel, err := selectProWorkspace(
		devsyConfig, workspaces, selectedWorkspace, params,
	)
	if err != nil {
		return resolvedWorkspace{}, err
	}
	if sel.handled {
		return resolvedWorkspace{provider: sel.provider, workspace: sel.workspace}, nil
	}

	return loadExistingWorkspace(devsyConfig, selectedWorkspace.ID, params.changeLastUsed)
}

type proWorkspaceSelection struct {
	provider  *providerpkg.ProviderConfig
	workspace *providerpkg.Workspace
	handled   bool
}

func selectProWorkspace(
	devsyConfig *config.Config,
	workspaces []*providerpkg.Workspace,
	selectedWorkspace *providerpkg.Workspace,
	params selectWorkspaceParams,
) (proWorkspaceSelection, error) {
	for _, workspace := range workspaces {
		if workspace.ID != selectedWorkspace.ID || !workspace.IsPro() {
			continue
		}
		providerConfig, err := importSelectedProWorkspace(devsyConfig, workspace, params)
		if err != nil {
			return proWorkspaceSelection{}, err
		}
		return proWorkspaceSelection{
			provider:  providerConfig,
			workspace: workspace,
			handled:   true,
		}, nil
	}

	return proWorkspaceSelection{}, nil
}

// listSelectableWorkspaces lists the candidate workspaces, most recently used
// first.
func listSelectableWorkspaces(
	ctx context.Context,
	devsyConfig *config.Config,
	params selectWorkspaceParams,
) ([]*providerpkg.Workspace, error) {
	var (
		workspaces []*providerpkg.Workspace
		err        error
	)
	if params.localOnly {
		workspaces, err = ListLocalWorkspaces(devsyConfig.DefaultContext, false)
	} else {
		workspaces, err = List(ctx, devsyConfig, false, params.owner)
	}
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	slices.SortStableFunc(workspaces, func(a, b *providerpkg.Workspace) int {
		return cmp.Compare(b.LastUsedTimestamp.Unix(), a.LastUsedTimestamp.Unix())
	})

	return workspaces, nil
}

func promptWorkspaceSelection(workspaces []*providerpkg.Workspace) (*providerpkg.Workspace, error) {
	options := make([]huh.Option[*providerpkg.Workspace], 0, len(workspaces))
	for _, workspace := range workspaces {
		key := workspace.ID
		if workspace.IsPro() && workspace.Pro.DisplayName != "" {
			key = fmt.Sprintf("%s (%s)", workspace.Pro.DisplayName, workspace.ID)
		}
		options = append(options, huh.NewOption(key, workspace))
	}

	var selectedWorkspace *providerpkg.Workspace
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[*providerpkg.Workspace]().
				Title("Select a workspace from the list below").
				Options(options...).
				Value(&selectedWorkspace),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	if selectedWorkspace == nil {
		return nil, errors.New("no workspace selected")
	}

	return selectedWorkspace, nil
}

func importSelectedProWorkspace(
	devsyConfig *config.Config,
	workspace *providerpkg.Workspace,
	params selectWorkspaceParams,
) (*providerpkg.ProviderConfig, error) {
	if workspace.SSHConfigPath == "" && params.sshConfigPath != "" {
		workspace.SSHConfigPath = params.sshConfigPath
	}
	if workspace.SSHConfigIncludePath == "" && params.sshConfigIncludePath != "" {
		workspace.SSHConfigIncludePath = params.sshConfigIncludePath
	}
	workspace.Imported = true
	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return nil, fmt.Errorf("save workspace config %q: %w", workspace.ID, err)
	}

	providerConfig, err := providerpkg.LoadProviderConfig(
		devsyConfig.DefaultContext,
		workspace.Provider.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("load provider config %q: %w", workspace.Provider.Name, err)
	}

	return providerConfig, nil
}

func loadExistingWorkspace(
	devsyConfig *config.Config,
	workspaceID string,
	changeLastUsed bool,
) (resolvedWorkspace, error) {
	workspaceConfig, err := providerpkg.LoadWorkspaceConfig(devsyConfig.DefaultContext, workspaceID)
	if err != nil {
		return resolvedWorkspace{}, err
	}

	providerWithOptions, err := FindProvider(devsyConfig, workspaceConfig.Provider.Name)
	if err != nil {
		return resolvedWorkspace{}, err
	}

	if changeLastUsed {
		workspaceConfig.LastUsedTimestamp = types.Now()
		if err := providerpkg.SaveWorkspaceConfig(workspaceConfig); err != nil {
			return resolvedWorkspace{}, err
		}
	}

	var machineConfig *providerpkg.Machine
	if workspaceConfig.Machine.ID != "" {
		machineConfig, err = providerpkg.LoadMachineConfig(
			workspaceConfig.Context,
			workspaceConfig.Machine.ID,
		)
		if err != nil {
			return resolvedWorkspace{}, fmt.Errorf("load machine config: %w", err)
		}
	}

	return resolvedWorkspace{
		provider:  providerWithOptions.Config,
		workspace: workspaceConfig,
		machine:   machineConfig,
	}, nil
}

type proInstanceParams struct {
	ctx          context.Context
	devsyConfig  *config.Config
	providerName string
	workspace    *providerpkg.Workspace
	stdin        io.Reader
	stdout       io.Writer
	stderr       io.Writer
}

func resolveProInstance(params proInstanceParams) error {
	foundProvider, err := FindProvider(params.devsyConfig, params.providerName)
	if err != nil {
		return err
	}

	workspaceClient, err := getWorkspaceClient(
		params.devsyConfig,
		foundProvider.Config,
		params.workspace,
		nil,
	)
	if err != nil {
		return err
	}

	if c, ok := workspaceClient.(RemoteCreator); ok {
		return c.Create(params.ctx, params.stdin, params.stdout, params.stderr)
	}

	return fmt.Errorf(
		"client %T for provider %q does not implement RemoteCreator",
		workspaceClient,
		params.providerName,
	)
}

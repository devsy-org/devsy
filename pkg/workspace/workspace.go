package workspace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
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

// maxWorkspaceIDLength bounds a user-provided workspace name.
const maxWorkspaceIDLength = 48

var errProvideWorkspaceArg = errors.New(
	"provide a workspace name, e.g. 'devsy workspace up ./my-folder', " +
		"'devsy workspace up github.com/my-org/my-repo' or 'devsy workspace up ubuntu'")

// ErrWorkspaceNotFound maps to the Retryable exit code, which the backhaul SSH
// command retries on.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// RemoteCreator defines the interface for clients that support remote workspace creation.
// This interface is implemented by ProxyClient and DaemonClient to enable workspace
// creation on remote platforms.
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
	SSHConfigPath        string
	SSHConfigIncludePath string
	Source               *providerpkg.WorkspaceSource
	UID                  string
	ChangeLastUsed       bool
	Owner                platform.OwnerFilter
}

// Resolve takes the `devsy up|build` CLI input and either finds an existing
// workspace or creates a new one, returning a ready-to-use client.
func Resolve(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
) (client.BaseWorkspaceClient, error) {
	if err := validateDesiredID(params.DesiredID); err != nil {
		return nil, err
	}

	provider, workspace, machine, err := resolveWorkspace(ctx, devsyConfig, params)
	if err != nil {
		return nil, err
	}

	workspace, err = ideparse.RefreshIDEOptions(
		devsyConfig,
		workspace,
		params.IDE,
		params.IDEOptions,
	)
	if err != nil {
		return nil, err
	}

	if err := applyDevContainerOverrides(workspace, params); err != nil {
		return nil, err
	}

	workspaceClient, err := getWorkspaceClient(devsyConfig, provider, workspace, machine)
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

// validateDesiredID enforces the workspace naming rules: only lower case
// letters, numbers or dashes, and at most maxWorkspaceIDLength characters. An
// empty id (the common case) is always valid.
func validateDesiredID(desiredID string) error {
	if desiredID == "" {
		return nil
	}
	if providerpkg.ProviderNameRegEx.MatchString(desiredID) {
		return fmt.Errorf("workspace name can only include lower case letters, numbers or dashes")
	}
	if len(desiredID) > maxWorkspaceIDLength {
		return fmt.Errorf(
			"workspace name cannot be longer than %d characters",
			maxWorkspaceIDLength,
		)
	}
	return nil
}

// applyDevContainerOverrides persists CLI-provided dev container overrides onto
// the workspace. It also re-saves a container-source workspace so its config is
// written before the client is built.
func applyDevContainerOverrides(workspace *providerpkg.Workspace, params ResolveParams) error {
	changed := false
	if params.DevContainerImage != "" && workspace.DevContainerImage != params.DevContainerImage {
		workspace.DevContainerImage = params.DevContainerImage
		changed = true
	}
	if params.DevContainerPath != "" && workspace.DevContainerPath != params.DevContainerPath {
		workspace.DevContainerPath = params.DevContainerPath
		changed = true
	}

	// A container-source workspace is always re-persisted so its config exists on
	// disk before the client is built.
	if !changed && workspace.Source.Container == "" {
		return nil
	}

	if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}

	return nil
}

// getWorkspaceClient builds the client matching the provider kind.
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

// Get tries to retrieve an already existing workspace. With no args the user is
// prompted to select one interactively.
func Get(ctx context.Context, opts GetOptions) (client.BaseWorkspaceClient, error) {
	if len(opts.Args) == 0 {
		provider, workspace, machine, err := selectWorkspace(
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

		return getWorkspaceClient(opts.DevsyConfig, provider, workspace, machine)
	}

	workspace, err := findWorkspaceByArgs(ctx, opts)
	if err != nil {
		return nil, err
	}
	if workspace == nil {
		return nil, fmt.Errorf("%w for args: %v", ErrWorkspaceNotFound, opts.Args)
	}

	provider, workspace, machine, err := loadExistingWorkspace(
		opts.DevsyConfig,
		workspace.ID,
		opts.ChangeLastUsed,
	)
	if err != nil {
		return nil, err
	}

	return getWorkspaceClient(opts.DevsyConfig, provider, workspace, machine)
}

func findWorkspaceByArgs(ctx context.Context, opts GetOptions) (*providerpkg.Workspace, error) {
	if opts.LocalOnly {
		return findLocalWorkspace(opts.DevsyConfig, opts.Args, ""), nil
	}
	return findWorkspace(ctx, opts.DevsyConfig, opts.Args, "", opts.Owner)
}

// Exists checks if the given workspace already exists and returns its ID, or an
// empty string if not found.
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

// resolveWorkspace finds an existing workspace matching the params, or creates a
// new one. On creation failure the freshly-created workspace folder is removed.
func resolveWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	// With no args, either load the desired workspace by id or prompt for one.
	if len(params.Args) == 0 {
		if params.DesiredID != "" {
			workspace, err := findWorkspace(ctx, devsyConfig, nil, params.DesiredID, params.Owner)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("find workspace: %w", err)
			}
			if workspace == nil {
				return nil, nil, nil, fmt.Errorf("workspace %s doesn't exist", params.DesiredID)
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

	isLocalPath, name := file.IsLocalDir(params.Args[0])
	workspaceID := ToID(name)

	// Reuse an existing workspace if the desired or derived id already exists.
	if params.DesiredID != "" {
		if Exists(ctx, devsyConfig, nil, params.DesiredID, params.Owner) != "" {
			log.Debugf("workspace ID already exists: desiredID=%s", params.DesiredID)
			return loadExistingWorkspace(devsyConfig, params.DesiredID, params.ChangeLastUsed)
		}
		workspaceID = params.DesiredID
	} else if Exists(ctx, devsyConfig, nil, workspaceID, params.Owner) != "" {
		log.Debugf("workspace already exists: workspaceID=%s", workspaceID)
		return loadExistingWorkspace(devsyConfig, workspaceID, params.ChangeLastUsed)
	}

	provider, workspace, machine, err := createWorkspace(ctx, devsyConfig, createWorkspaceParams{
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
		return nil, nil, nil, err
	}

	return provider, workspace, machine, nil
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
// machine or remote (pro) instance the provider requires.
func createWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params createWorkspaceParams,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	provider, err := loadInitializedProvider(devsyConfig)
	if err != nil {
		return nil, nil, nil, err
	}

	workspace, err := resolveWorkspaceConfig(
		ctx,
		provider,
		devsyConfig,
		resolveWorkspaceConfigParams{
			name:                 params.name,
			workspaceID:          params.workspaceID,
			source:               params.source,
			isLocalPath:          params.isLocalPath,
			sshConfigPath:        params.sshConfigPath,
			sshConfigIncludePath: params.sshConfigIncludePath,
			uid:                  params.uid,
		},
	)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := assignDesiredMachine(provider, workspace, params.desiredMachine); err != nil {
		return nil, nil, nil, err
	}

	var machineConfig *providerpkg.Machine
	switch {
	case provider.Config.IsMachineProvider() && workspace.Machine.ID == "":
		machineConfig, err = provisionManagedMachine(ctx, machineProvisionParams{
			devsyConfig:         devsyConfig,
			provider:            provider,
			workspace:           workspace,
			providerUserOptions: params.providerUserOptions,
		})
	case provider.Config.IsProxyProvider() || provider.Config.IsDaemonProvider():
		workspace, err = resolveProWorkspace(ctx, devsyConfig, provider, workspace)
	default:
		machineConfig, err = saveAndLoadExistingMachine(provider, workspace)
	}
	if err != nil {
		return nil, nil, nil, err
	}

	return provider.Config, workspace, machineConfig, nil
}

// loadInitializedProvider returns the default provider, requiring it to be
// initialized.
func loadInitializedProvider(devsyConfig *config.Config) (*ProviderWithOptions, error) {
	provider, _, err := LoadProviders(devsyConfig)
	if err != nil {
		return nil, err
	}
	if provider.State == nil || !provider.State.Initialized {
		return nil, fmt.Errorf(
			"provider %q is not initialized, make sure to run 'devsy provider init %s' "+
				"at least once before using this provider",
			provider.Config.Name,
			provider.Config.Name,
		)
	}
	return provider, nil
}

// assignDesiredMachine attaches a caller-specified, pre-existing machine to the
// workspace, validating that the provider supports machines and the machine
// exists.
func assignDesiredMachine(
	provider *ProviderWithOptions,
	workspace *providerpkg.Workspace,
	desiredMachine string,
) error {
	if desiredMachine == "" {
		return nil
	}
	if !provider.Config.IsMachineProvider() {
		return fmt.Errorf(
			"provider %s cannot create servers and cannot be used",
			provider.Config.Name,
		)
	}
	if !providerpkg.MachineExists(workspace.Context, desiredMachine) {
		return fmt.Errorf("server %s doesn't exist and cannot be used", desiredMachine)
	}

	workspace.Machine = providerpkg.WorkspaceMachineConfig{ID: desiredMachine}
	return nil
}

// machineProvisionParams bundles the inputs required to provision a workspace's
// backing machine.
type machineProvisionParams struct {
	devsyConfig         *config.Config
	provider            *ProviderWithOptions
	workspace           *providerpkg.Workspace
	providerUserOptions []string
}

// provisionManagedMachine assigns a machine id to a machine-provider workspace
// (a shared single machine or a new auto-deleted one), persists the config, and
// either reuses an existing machine or creates a fresh one.
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
		log.Infof("Reuse existing machine %q for workspace %q", workspace.Machine.ID, workspace.ID)
		machineConfig, err := providerpkg.LoadMachineConfig(workspace.Context, workspace.Machine.ID)
		if err != nil {
			return nil, fmt.Errorf("load machine config: %w", err)
		}
		return machineConfig, nil
	}

	return createManagedMachine(ctx, params)
}

// createManagedMachine creates the machine folder and drives the machine client
// through option refresh and creation, cleaning up the folder on any failure
// after it has been created.
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

// cleanupMachineFolder best-effort removes a machine folder and returns the
// original cause so it can be surfaced to the caller.
func cleanupMachineFolder(machineConfig *providerpkg.Machine, cause error) error {
	_ = clientimplementation.DeleteMachineFolder(machineConfig.Context, machineConfig.ID)
	return cause
}

// resolveProWorkspace persists the workspace, hands provisioning to the remote
// pro process, then reloads the config the provider wrote back.
//
// The pro process can't communicate with us directly: it needs os i/o to render
// the form in CLI mode, so we save the config, tell the provider where it lives,
// let it update the config, and read it back into workspace state here.
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

// saveAndLoadExistingMachine persists the workspace and, for machine providers
// with a pre-assigned machine, loads that machine's config.
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

// resolveWorkspaceConfig builds a new workspace config and determines its source
// (explicit, local folder, git repository or image).
func resolveWorkspaceConfig(
	ctx context.Context,
	defaultProvider *ProviderWithOptions,
	devsyConfig *config.Config,
	params resolveWorkspaceConfigParams,
) (*providerpkg.Workspace, error) {
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

	source, picture := resolveWorkspaceSource(ctx, params)
	workspace.Source = source
	workspace.Picture = picture

	return workspace, nil
}

// resolveWorkspaceSource classifies the workspace source from the params,
// probing git/image registries as needed. It returns the source and, for git
// repositories, an associated project picture.
func resolveWorkspaceSource(
	ctx context.Context,
	params resolveWorkspaceConfigParams,
) (providerpkg.WorkspaceSource, string) {
	// explicit source wins
	if params.source != nil {
		return *params.source, ""
	}

	// local folder
	if params.isLocalPath {
		return providerpkg.WorkspaceSource{LocalFolder: params.name}, ""
	}

	// git repository (explicit .git suffix or a repository that responds)
	info := git.NormalizeRepository(params.name)
	if strings.HasSuffix(params.name, ".git") ||
		git.PingRepository(info.Repository, git.GetDefaultExtraEnv(false)) {
		return providerpkg.WorkspaceSource{
			GitRepository:  info.Repository,
			GitPRReference: info.PR,
			GitBranch:      info.Branch,
			GitCommit:      info.Commit,
			GitSubPath:     info.SubPath,
		}, getProjectImage(params.name)
	}

	// image
	if _, err := image.GetImage(ctx, params.name); err == nil {
		return providerpkg.WorkspaceSource{Image: params.name}, ""
	}

	// fall back to a git repository derived from the normalized info, defaulting
	// the repository to the raw name when normalization produced none
	return providerpkg.WorkspaceSource{
		GitRepository:  cmp.Or(info.Repository, params.name),
		GitPRReference: info.PR,
		GitBranch:      info.Branch,
		GitCommit:      info.Commit,
		GitSubPath:     info.SubPath,
	}, ""
}

// ensureWorkspaceID returns the workspace id, deriving it from the first arg
// when not explicitly provided. It returns an empty string when neither an id
// nor args are available.
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

// findLocalWorkspace looks up a workspace among the locally-configured ones.
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
		log.Debugf("failed to list workspaces: %v", err)
		return nil
	}

	for _, workspace := range allWorkspaces {
		if workspace.ID == workspaceID {
			return workspace
		}
	}

	return nil
}

// findWorkspace looks up a workspace among all known ones (local and remote). A
// matching pro workspace is marked imported and persisted locally.
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
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	for _, workspace := range allWorkspaces {
		if workspace.ID != workspaceID {
			continue
		}

		if workspace.IsPro() {
			workspace.Imported = true
			if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
				return nil, fmt.Errorf("failed to save workspace config: %w", err)
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

// selectWorkspace prompts the user to pick a workspace from the available ones.
func selectWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params selectWorkspaceParams,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	if !terminal.IsTerminalIn {
		return nil, nil, nil, errProvideWorkspaceArg
	}

	workspaces, err := listSelectableWorkspaces(ctx, devsyConfig, params)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(workspaces) == 0 {
		return nil, nil, nil, errors.Join(ErrNoWorkspaceFound, errProvideWorkspaceArg)
	}

	selectedWorkspace, err := promptWorkspaceSelection(workspaces)
	if err != nil {
		return nil, nil, nil, err
	}

	// A selected pro workspace is imported and saved locally before use.
	for _, workspace := range workspaces {
		if workspace.ID != selectedWorkspace.ID || !workspace.IsPro() {
			continue
		}
		providerConfig, err := importSelectedProWorkspace(devsyConfig, workspace, params)
		if err != nil {
			return nil, nil, nil, err
		}
		return providerConfig, workspace, nil, nil
	}

	return loadExistingWorkspace(devsyConfig, selectedWorkspace.ID, params.changeLastUsed)
}

// listSelectableWorkspaces lists the candidate workspaces, sorted by most
// recently used first.
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

	sort.SliceStable(workspaces, func(i, j int) bool {
		return workspaces[i].LastUsedTimestamp.Unix() > workspaces[j].LastUsedTimestamp.Unix()
	})

	return workspaces, nil
}

// promptWorkspaceSelection renders the interactive selection form and returns
// the chosen workspace.
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
		return nil, fmt.Errorf("no workspace selected")
	}

	return selectedWorkspace, nil
}

// importSelectedProWorkspace persists the selected pro workspace locally
// (carrying over ssh config paths) and returns its provider config.
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
		return nil, fmt.Errorf("save workspace config for workspace %q: %w", workspace.ID, err)
	}

	providerConfig, err := providerpkg.LoadProviderConfig(
		devsyConfig.DefaultContext,
		workspace.Provider.Name,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"load provider config for workspace %q with provider %q: %w",
			workspace.ID,
			workspace.Provider.Name,
			err,
		)
	}

	return providerConfig, nil
}

// loadExistingWorkspace loads a workspace config, its provider and any backing
// machine, optionally bumping the last-used timestamp.
func loadExistingWorkspace(
	devsyConfig *config.Config,
	workspaceID string,
	changeLastUsed bool,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	workspaceConfig, err := providerpkg.LoadWorkspaceConfig(devsyConfig.DefaultContext, workspaceID)
	if err != nil {
		return nil, nil, nil, err
	}

	providerWithOptions, err := FindProvider(devsyConfig, workspaceConfig.Provider.Name)
	if err != nil {
		return nil, nil, nil, err
	}

	if changeLastUsed {
		workspaceConfig.LastUsedTimestamp = types.Now()
		if err := providerpkg.SaveWorkspaceConfig(workspaceConfig); err != nil {
			return nil, nil, nil, err
		}
	}

	var machineConfig *providerpkg.Machine
	if workspaceConfig.Machine.ID != "" {
		machineConfig, err = providerpkg.LoadMachineConfig(
			workspaceConfig.Context,
			workspaceConfig.Machine.ID,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load machine config: %w", err)
		}
	}

	return providerWithOptions.Config, workspaceConfig, machineConfig, nil
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

// resolveProInstance drives remote workspace creation via a provider client
// that implements RemoteCreator.
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

	// This should never happen - indicates a programming error where a proxy/daemon provider
	// client does not implement the RemoteCreator interface.
	return fmt.Errorf(
		"internal error: client %T for provider %q does not implement RemoteCreator interface",
		workspaceClient,
		params.providerName,
	)
}

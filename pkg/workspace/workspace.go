package workspace

import (
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
	"github.com/devsy-org/devsy/pkg/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/log"
	"github.com/devsy-org/log/terminal"
)

var errProvideWorkspaceArg = errors.New(
	"please provide a workspace name, e.g. 'devsy up ./my-folder', " +
		"'devsy up github.com/my-org/my-repo' or 'devsy up ubuntu'")

// RemoteCreator defines the interface for clients that support remote workspace creation.
// This interface is implemented by ProxyClient and DaemonClient to enable workspace
// creation on remote platforms.
type RemoteCreator interface {
	Create(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error
}

// Resolve takes the `devsy up|build` CLI input and either finds an existing workspace or creates a new one.
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

func Resolve(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
	log log.Logger,
) (client.BaseWorkspaceClient, error) {
	// verify desired id
	if params.DesiredID != "" {
		if providerpkg.ProviderNameRegEx.MatchString(params.DesiredID) {
			return nil, fmt.Errorf(
				"workspace name can only include lower case letters, numbers or dashes",
			)
		} else if len(params.DesiredID) > 48 {
			return nil, fmt.Errorf("workspace name cannot be longer than 48 characters")
		}
	}

	// resolve workspace
	provider, workspace, machine, err := resolveWorkspace(ctx, devsyConfig, params, log)
	if err != nil {
		return nil, err
	}

	// configure ide
	workspace, err = ideparse.RefreshIDEOptions(
		devsyConfig,
		workspace,
		params.IDE,
		params.IDEOptions,
	)
	if err != nil {
		return nil, err
	}

	// configure dev container source
	if params.DevContainerImage != "" && workspace.DevContainerImage != params.DevContainerImage {
		workspace.DevContainerImage = params.DevContainerImage

		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, fmt.Errorf("save workspace: %w", err)
		}
	}

	// configure dev container source
	if params.DevContainerPath != "" && workspace.DevContainerPath != params.DevContainerPath {
		workspace.DevContainerPath = params.DevContainerPath

		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, fmt.Errorf("save workspace: %w", err)
		}
	}

	// configure dev container source
	if workspace.Source.Container != "" {
		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, fmt.Errorf("save workspace: %w", err)
		}
	}

	// create workspace client
	client, err := getWorkspaceClient(devsyConfig, provider, workspace, machine, log)
	if err != nil {
		return nil, err
	}

	// refresh provider options
	err = client.RefreshOptions(ctx, params.ProviderUserOptions, params.ReconfigureProvider)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getWorkspaceClient(
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
	workspace *providerpkg.Workspace,
	machine *providerpkg.Machine,
	log log.Logger,
) (client.BaseWorkspaceClient, error) {
	if provider.IsProxyProvider() {
		return clientimplementation.NewProxyClient(devsyConfig, provider, workspace, log)
	} else if provider.IsDaemonProvider() {
		return daemonclient.New(devsyConfig, provider, workspace, log)
	} else {
		return clientimplementation.NewWorkspaceClient(
			devsyConfig,
			provider,
			workspace,
			machine,
			log,
		)
	}
}

// GetOptions holds the parameters for retrieving an existing workspace.
type GetOptions struct {
	DevsyConfig    *config.Config
	Args           []string
	ChangeLastUsed bool
	Owner          platform.OwnerFilter
	LocalOnly      bool
	Log            log.Logger
}

// Get tries to retrieve an already existing workspace.
func Get(ctx context.Context, opts GetOptions) (client.BaseWorkspaceClient, error) {
	if len(opts.Args) == 0 {
		provider, workspace, machine, err := selectWorkspace(
			ctx,
			opts.DevsyConfig,
			selectWorkspaceParams{
				changeLastUsed:       opts.ChangeLastUsed,
				sshConfigPath:        "",
				sshConfigIncludePath: "",
				owner:                opts.Owner,
				localOnly:            opts.LocalOnly,
			},
			opts.Log,
		)
		if err != nil {
			return nil, err
		}

		return getWorkspaceClient(opts.DevsyConfig, provider, workspace, machine, opts.Log)
	}

	workspace := findWorkspaceByArgs(ctx, opts)
	if workspace == nil {
		return nil, fmt.Errorf("workspace not found for args: %v", opts.Args)
	}

	provider, workspace, machine, err := loadExistingWorkspace(
		opts.DevsyConfig,
		workspace.ID,
		opts.ChangeLastUsed,
		opts.Log,
	)
	if err != nil {
		return nil, err
	}

	return getWorkspaceClient(opts.DevsyConfig, provider, workspace, machine, opts.Log)
}

func findWorkspaceByArgs(
	ctx context.Context,
	opts GetOptions,
) *providerpkg.Workspace {
	if opts.LocalOnly {
		return findLocalWorkspace(opts.DevsyConfig, opts.Args, "", opts.Log)
	}
	return findWorkspace(ctx, opts.DevsyConfig, opts.Args, "", opts.Owner, opts.Log)
}

// Exists checks if the given workspace already exists.
func Exists(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
	owner platform.OwnerFilter,
	log log.Logger,
) string {
	workspace := findWorkspace(ctx, devsyConfig, args, workspaceID, owner, log)
	if workspace == nil {
		return ""
	}

	return workspace.ID
}

func resolveWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params ResolveParams,
	log log.Logger,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	// check if we have no args
	if len(params.Args) == 0 {
		if params.DesiredID != "" {
			workspace := findWorkspace(ctx, devsyConfig, nil, params.DesiredID, params.Owner, log)
			if workspace == nil {
				return nil, nil, nil, fmt.Errorf("workspace %s doesn't exist", params.DesiredID)
			}
			return loadExistingWorkspace(devsyConfig, workspace.ID, params.ChangeLastUsed, log)
		}

		return selectWorkspace(ctx, devsyConfig, selectWorkspaceParams{
			changeLastUsed:       params.ChangeLastUsed,
			sshConfigPath:        params.SSHConfigPath,
			sshConfigIncludePath: params.SSHConfigIncludePath,
			owner:                params.Owner,
		}, log)
	}

	// check if workspace already exists
	isLocalPath, name := file.IsLocalDir(params.Args[0])

	// convert to id
	workspaceID := ToID(name)

	// check if desired id already exists
	if params.DesiredID != "" {
		if Exists(ctx, devsyConfig, nil, params.DesiredID, params.Owner, log) != "" {
			log.Debugf("workspace ID already exists: desiredID=%s", params.DesiredID)
			return loadExistingWorkspace(devsyConfig, params.DesiredID, params.ChangeLastUsed, log)
		}

		// set desired id
		workspaceID = params.DesiredID
	} else if Exists(ctx, devsyConfig, nil, workspaceID, params.Owner, log) != "" {
		log.Debugf("workspace already exists: workspaceID=%s", workspaceID)
		return loadExistingWorkspace(devsyConfig, workspaceID, params.ChangeLastUsed, log)
	}

	// create workspace
	provider, workspace, machine, err := createWorkspace(
		ctx,
		devsyConfig,
		createWorkspaceParams{
			workspaceID:          workspaceID,
			name:                 name,
			desiredMachine:       params.DesiredMachine,
			providerUserOptions:  params.ProviderUserOptions,
			sshConfigPath:        params.SSHConfigPath,
			sshConfigIncludePath: params.SSHConfigIncludePath,
			source:               params.Source,
			isLocalPath:          isLocalPath,
			uid:                  params.UID,
		},
		log,
	)
	if err != nil {
		_ = clientimplementation.DeleteWorkspaceFolder(
			clientimplementation.DeleteWorkspaceFolderParams{
				Context:              devsyConfig.DefaultContext,
				WorkspaceID:          workspaceID,
				SSHConfigPath:        params.SSHConfigPath,
				SSHConfigIncludePath: params.SSHConfigIncludePath,
			},
			log,
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

func createWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	params createWorkspaceParams,
	log log.Logger,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	// get default provider
	provider, _, err := LoadProviders(devsyConfig, log)
	if err != nil {
		return nil, nil, nil, err
	} else if provider.State == nil || !provider.State.Initialized {
		return nil, nil, nil, fmt.Errorf(
			"provider '%s' is not initialized, please make sure to run 'devsy provider use %s' "+
				"at least once before using this provider",
			provider.Config.Name,
			provider.Config.Name,
		)
	}

	// resolve workspace
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

	// set server
	if params.desiredMachine != "" {
		if !provider.Config.IsMachineProvider() {
			return nil, nil, nil, fmt.Errorf(
				"provider %s cannot create servers and cannot be used",
				provider.Config.Name,
			)
		}

		// check if server exists
		if !providerpkg.MachineExists(workspace.Context, params.desiredMachine) {
			return nil, nil, nil, fmt.Errorf(
				"server %s doesn't exist and cannot be used",
				params.desiredMachine,
			)
		}

		// configure server for workspace
		workspace.Machine = providerpkg.WorkspaceMachineConfig{
			ID: params.desiredMachine,
		}
	}

	// create a new machine
	var machineConfig *providerpkg.Machine
	if provider.Config.IsMachineProvider() && workspace.Machine.ID == "" {
		// create a new machine
		if provider.State != nil && provider.State.SingleMachine {
			workspace.Machine.ID = SingleMachineName(devsyConfig, provider.Config.Name, log)
		} else {
			workspace.Machine.ID = encoding.CreateNewUIDShort(workspace.ID)
			workspace.Machine.AutoDelete = true
		}

		// save workspace config
		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("save config: %w", err)
		}

		// only create machine if it does not exist yet
		if !providerpkg.MachineExists(devsyConfig.DefaultContext, workspace.Machine.ID) {
			// create machine folder
			machineConfig, err = createMachine(
				workspace.Context,
				workspace.Machine.ID,
				provider.Config.Name,
			)
			if err != nil {
				return nil, nil, nil, err
			}

			// create machine
			machineClient, err := clientimplementation.NewMachineClient(
				devsyConfig,
				provider.Config,
				machineConfig,
				log,
			)
			if err != nil {
				_ = clientimplementation.DeleteMachineFolder(
					machineConfig.Context,
					machineConfig.ID,
				)
				return nil, nil, nil, err
			}

			// refresh options
			err = machineClient.RefreshOptions(ctx, params.providerUserOptions, false)
			if err != nil {
				_ = clientimplementation.DeleteMachineFolder(
					machineConfig.Context,
					machineConfig.ID,
				)
				return nil, nil, nil, err
			}

			// create machine
			err = machineClient.Create(ctx)
			if err != nil {
				_ = clientimplementation.DeleteMachineFolder(
					machineConfig.Context,
					machineConfig.ID,
				)
				return nil, nil, nil, err
			}
		} else {
			log.Infof(
				"Reuse existing machine '%s' for workspace '%s'",
				workspace.Machine.ID,
				workspace.ID,
			)

			// load machine config
			machineConfig, err = providerpkg.LoadMachineConfig(
				workspace.Context,
				workspace.Machine.ID,
			)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("load machine config: %w", err)
			}
		}
	} else if provider.Config.IsProxyProvider() || provider.Config.IsDaemonProvider() {
		// We'll do have to do a bit of mumbo jumbo here because the pro process can't communicate with us directly.
		// It needs os i/o to render the form in CLI mode so we can't go with our typical setup.
		// Instead we first save the config, tell the provider where it lives, it updates it,
		// then we read it again and update to workspace state here
		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("save config: %w", err)
		}

		err := resolveProInstance(proInstanceParams{
			ctx:          ctx,
			devsyConfig:  devsyConfig,
			providerName: provider.Config.Name,
			workspace:    workspace,
			stdin:        os.Stdin,
			stdout:       os.Stdout,
			stderr:       os.Stderr,
			log:          log,
		})
		if err != nil {
			return nil, nil, nil, err
		}

		workspace, err = providerpkg.LoadWorkspaceConfig(workspace.Context, workspace.ID)
		if err != nil {
			return nil, nil, nil, err
		}
	} else {
		// save workspace config
		err = providerpkg.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("save config: %w", err)
		}

		// load machine config
		if provider.Config.IsMachineProvider() && workspace.Machine.ID != "" {
			machineConfig, err = providerpkg.LoadMachineConfig(
				workspace.Context,
				workspace.Machine.ID,
			)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("load machine config: %w", err)
			}
		}
	}

	return provider.Config, workspace, machineConfig, nil
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

	// outside source set?
	if params.source != nil {
		workspace.Source = *params.source
		return workspace, nil
	}

	// is local folder?
	if params.isLocalPath {
		workspace.Source = providerpkg.WorkspaceSource{
			LocalFolder: params.name,
		}
		return workspace, nil
	}

	// is git?
	gitRepository, gitPRReference, gitBranch, gitCommit, gitSubdir := git.NormalizeRepository(
		params.name,
	)
	if strings.HasSuffix(params.name, ".git") ||
		git.PingRepository(gitRepository, git.GetDefaultExtraEnv(false)) {
		workspace.Picture = getProjectImage(params.name)
		workspace.Source = providerpkg.WorkspaceSource{
			GitRepository:  gitRepository,
			GitPRReference: gitPRReference,
			GitBranch:      gitBranch,
			GitCommit:      gitCommit,
			GitSubPath:     gitSubdir,
		}

		return workspace, nil
	}

	// is image?
	_, err := image.GetImage(ctx, params.name)
	if err == nil {
		workspace.Source = providerpkg.WorkspaceSource{
			Image: params.name,
		}
		return workspace, nil
	}

	// fall back to Git repository
	workspace.Source = providerpkg.WorkspaceSource{GitRepository: params.name}
	if gitRepository != "" {
		workspace.Source.GitRepository = gitRepository
	}
	if gitPRReference != "" {
		workspace.Source.GitPRReference = gitPRReference
	}
	if gitBranch != "" {
		workspace.Source.GitBranch = gitBranch
	}
	if gitCommit != "" {
		workspace.Source.GitCommit = gitCommit
	}
	if gitSubdir != "" {
		workspace.Source.GitSubPath = gitSubdir
	}

	return workspace, nil
}

func ensureWorkspaceID(args []string, workspaceID string) string {
	if len(args) == 0 && workspaceID == "" {
		return ""
	}

	if workspaceID == "" {
		// check if workspace already exists
		_, name := file.IsLocalDir(args[0])

		// convert to id
		workspaceID = ToID(name)
	}

	return workspaceID
}

func findLocalWorkspace(
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
	log log.Logger,
) *providerpkg.Workspace {
	workspaceID = ensureWorkspaceID(args, workspaceID)
	if workspaceID == "" {
		return nil
	}

	allWorkspaces, err := ListLocalWorkspaces(devsyConfig.DefaultContext, false, log)
	if err != nil {
		log.Debugf("failed to list workspaces: %v", err)
		return nil
	}

	for _, workspace := range allWorkspaces {
		if workspace.ID != workspaceID {
			continue
		}
		return workspace
	}

	return nil
}

func findWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
	workspaceID string,
	owner platform.OwnerFilter,
	log log.Logger,
) *providerpkg.Workspace {
	workspaceID = ensureWorkspaceID(args, workspaceID)
	if workspaceID == "" {
		return nil
	}

	allWorkspaces, err := List(ctx, devsyConfig, false, owner, log)
	if err != nil {
		log.Debugf("failed to list workspaces: %v", err)
		return nil
	}

	var retWorkspace *providerpkg.Workspace
	// already exists in all workspaces (including remote)?
	for _, workspace := range allWorkspaces {
		if workspace.ID != workspaceID {
			continue
		}

		if workspace.IsPro() {
			workspace.Imported = true
			err = providerpkg.SaveWorkspaceConfig(workspace)
			if err != nil {
				log.Debugf(
					"failed to save workspace config for workspace \"%s\" with provider \"%s\": %v",
					workspace.ID,
					workspace.Provider.Name,
					err,
				)
				return nil
			}
		}

		retWorkspace = workspace
		break
	}

	return retWorkspace
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
	log log.Logger,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	if !terminal.IsTerminalIn {
		return nil, nil, nil, errProvideWorkspaceArg
	}

	var (
		workspaces []*providerpkg.Workspace
		err        error
	)
	if params.localOnly {
		workspaces, err = ListLocalWorkspaces(devsyConfig.DefaultContext, false, log)
	} else {
		workspaces, err = List(ctx, devsyConfig, false, params.owner, log)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list workspaces: %w", err)
	}

	// sort by last used
	sort.SliceStable(workspaces, func(i, j int) bool {
		return workspaces[i].LastUsedTimestamp.Unix() > workspaces[j].LastUsedTimestamp.Unix()
	})

	// prepare form options
	options := []huh.Option[*providerpkg.Workspace]{}
	for _, workspace := range workspaces {
		key := workspace.ID
		if workspace.IsPro() && workspace.Pro.DisplayName != "" {
			key = fmt.Sprintf("%s (%s)", workspace.Pro.DisplayName, workspace.ID)
		}
		options = append(options, huh.NewOption(key, workspace))
	}
	if len(workspaces) == 0 {
		return nil, nil, nil, errors.Join(ErrNoWorkspaceFound, errProvideWorkspaceArg)
	}

	// create terminal form
	var selectedWorkspace *providerpkg.Workspace
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[*providerpkg.Workspace]().
				Title("Please select a workspace from the list below").
				Options(options...).
				Value(&selectedWorkspace),
		),
	)
	if err := form.Run(); err != nil {
		return nil, nil, nil, err
	}
	if selectedWorkspace == nil {
		return nil, nil, nil, fmt.Errorf("no workspace selected")
	}

	// if selected workspace is pro, save config locally
	for _, workspace := range workspaces {
		if workspace.ID == selectedWorkspace.ID && workspace.IsPro() {
			if workspace.SSHConfigPath == "" && params.sshConfigPath != "" {
				workspace.SSHConfigPath = params.sshConfigPath
			}
			if workspace.SSHConfigIncludePath == "" && params.sshConfigIncludePath != "" {
				workspace.SSHConfigIncludePath = params.sshConfigIncludePath
			}
			workspace.Imported = true
			if err := providerpkg.SaveWorkspaceConfig(workspace); err != nil {
				return nil, nil, nil, fmt.Errorf(
					"save workspace config for workspace \"%s\": %w",
					workspace.ID,
					err,
				)
			}

			providerConfig, err := providerpkg.LoadProviderConfig(
				devsyConfig.DefaultContext,
				workspace.Provider.Name,
			)
			if err != nil {
				return nil, nil, nil, fmt.Errorf(
					"load provider config for workspace \"%s\" with provider \"%s\": %w",
					workspace.ID,
					workspace.Provider.Name,
					err,
				)
			}

			return providerConfig, workspace, nil, nil
		}
	}

	// load workspace
	return loadExistingWorkspace(devsyConfig, selectedWorkspace.ID, params.changeLastUsed, log)
}

func loadExistingWorkspace(
	devsyConfig *config.Config,
	workspaceID string,
	changeLastUsed bool,
	log log.Logger,
) (*providerpkg.ProviderConfig, *providerpkg.Workspace, *providerpkg.Machine, error) {
	workspaceConfig, err := providerpkg.LoadWorkspaceConfig(
		devsyConfig.DefaultContext,
		workspaceID,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	providerWithOptions, err := FindProvider(devsyConfig, workspaceConfig.Provider.Name, log)
	if err != nil {
		return nil, nil, nil, err
	}

	// save workspace config
	if changeLastUsed {
		workspaceConfig.LastUsedTimestamp = types.Now()
		err = providerpkg.SaveWorkspaceConfig(workspaceConfig)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	// load machine config
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

	// create client
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
	log          log.Logger
}

func resolveProInstance(params proInstanceParams) error {
	foundProvider, err := FindProvider(params.devsyConfig, params.providerName, params.log)
	if err != nil {
		return err
	}

	workspaceClient, err := getWorkspaceClient(
		params.devsyConfig,
		foundProvider.Config,
		params.workspace,
		nil,
		params.log,
	)
	if err != nil {
		return err
	}

	if c, ok := workspaceClient.(RemoteCreator); ok {
		return c.Create(params.ctx, params.stdin, params.stdout, params.stderr)
	}

	// This should never happen - indicates a programming error where a proxy/daemon provider
	// client does not implement the RemoteCreator interface
	return fmt.Errorf(
		"internal error: client %T for provider %q does not implement RemoteCreator interface",
		workspaceClient,
		params.providerName,
	)
}

package up

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
)

func mergeDevsyUpOptions(baseOptions *provider2.CLIOptions) error {
	oldOptions := *baseOptions
	found, err := clientimplementation.DecodeOptionsFromEnv(
		config.EnvFlagsUp,
		baseOptions,
	)
	if err != nil {
		return fmt.Errorf("decode up options: %w", err)
	} else if found {
		baseOptions.WorkspaceEnv = append(oldOptions.WorkspaceEnv, baseOptions.WorkspaceEnv...)
		baseOptions.InitEnv = append(oldOptions.InitEnv, baseOptions.InitEnv...)
		baseOptions.PrebuildRepositories = append(
			oldOptions.PrebuildRepositories,
			baseOptions.PrebuildRepositories...)
		baseOptions.IDEOptions = append(oldOptions.IDEOptions, baseOptions.IDEOptions...)
	}

	err = clientimplementation.DecodePlatformOptionsFromEnv(&baseOptions.Platform)
	if err != nil {
		return fmt.Errorf("decode platform options: %w", err)
	}

	return nil
}

func mergeEnvFromFiles(baseOptions *provider2.CLIOptions) error {
	var variables []string
	for _, file := range baseOptions.WorkspaceEnvFile {
		envFromFile, err := config2.ParseKeyValueFile(file)
		if err != nil {
			return err
		}
		variables = append(variables, envFromFile...)
	}
	baseOptions.WorkspaceEnv = append(baseOptions.WorkspaceEnv, variables...)

	return nil
}

func (cmd *UpCmd) prepareClient(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) (client2.BaseWorkspaceClient, error) {
	if err := cmd.prepareClientEnvironment(devsyConfig, args, ctx); err != nil {
		return nil, err
	}

	source, err := cmd.parseWorkspaceSource()
	if err != nil {
		return nil, err
	}
	// Bootstrap credentials are resolved exclusively from sources that are
	// available before repository acquisition. Repository-owned sources are
	// deliberately not registered yet, which prevents circular clone auth.
	if err := cmd.prepareBootstrapGitToken(ctx, devsyConfig, source); err != nil {
		return nil, err
	}

	cmd.resolveSSHConfig(devsyConfig)
	args = cmd.ensureArgsForFromSnapshot(args)

	log.Debugf("up: resolving workspace with cmd.IDE=%q ide-launch=%q", cmd.IDE, cmd.IDELaunch)
	client, err := workspace2.Resolve(
		ctx,
		devsyConfig,
		cmd.resolveParams(args, source, devsyConfig),
	)
	if err != nil {
		return nil, err
	}

	// The workspace source (local folder, git repository, or image) is only
	// fully known once Resolve has determined it, e.g. from a positional
	// workspace argument rather than --source/--from-snapshot. Repository-
	// owned secret discovery must therefore happen after Resolve, using the
	// client's resolved WorkspaceConfig().Source, not the possibly-nil
	// source parsed above.
	if err := cmd.prepareResolvedWorkspaceSecrets(ctx, devsyConfig, client); err != nil {
		_ = client.Delete(ctx, client2.DeleteOptions{Force: true, IgnoreNotFound: true})
		return nil, err
	}

	if err := cmd.checkProviderUpdate(ctx, devsyConfig, client); err != nil {
		_ = client.Delete(ctx, client2.DeleteOptions{Force: true, IgnoreNotFound: true})
		return nil, err
	}
	return client, nil
}

func (cmd *UpCmd) prepareClientEnvironment(
	devsyConfig *config.Config,
	args []string,
	ctx context.Context,
) error {
	if err := mergeDevsyUpOptions(&cmd.CLIOptions); err != nil {
		return err
	}
	if cmd.Platform.Enabled {
		log.Debug("running in platform mode")
		log.Debug("using error output stream")
		config.MergeContextOptions(devsyConfig.Current(), os.Environ())
	}
	return cmd.validateFromSnapshot(ctx, args)
}

// prepareResolvedWorkspaceSecrets discovers repository-owned project secrets
// (e.g. SOPS sources declared in .devsy/config.yaml) using the workspace's
// fully resolved source and merges them with attached/explicit secrets.
// This must run after workspace2.Resolve, since a positional workspace
// argument's local-folder/git-repository/image classification is only known
// once Resolve has determined client.WorkspaceConfig().Source.
func (cmd *UpCmd) prepareResolvedWorkspaceSecrets(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	var source *provider2.WorkspaceSource
	if cfg := client.WorkspaceConfig(); cfg != nil {
		source = &cfg.Source
	}
	projectSecrets, err := cmd.discoverProjectSecrets(ctx, source)
	if err != nil {
		return err
	}
	return cmd.prepareSecretsWithProject(ctx, devsyConfig, projectSecrets)
}

// checkProviderUpdate checks for a provider update, unless running in platform mode.
func (cmd *UpCmd) checkProviderUpdate(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	if cmd.Platform.Enabled {
		return nil
	}
	proInstance := workspace2.GetProInstance(devsyConfig, client.Provider())
	return workspace2.CheckProviderUpdate(ctx, devsyConfig, proInstance)
}

// ensureArgsForFromSnapshot returns args unchanged unless --from-snapshot is
// set and args is empty, in which case it synthesizes a placeholder arg.
func (cmd *UpCmd) ensureArgsForFromSnapshot(args []string) []string {
	if cmd.FromSnapshot != "" && len(args) == 0 {
		return []string{cmd.FromSnapshot}
	}
	return args
}

func (cmd *UpCmd) resolveParams(
	args []string, source *provider2.WorkspaceSource, devsyConfig *config.Config,
) workspace2.ResolveParams {
	return workspace2.ResolveParams{
		IDE:                 cmd.IDE,
		IDEOptions:          cmd.IDEOptions,
		Args:                args,
		DesiredID:           cmd.ID,
		DesiredMachine:      cmd.Machine,
		ProviderUserOptions: cmd.ProviderOptions,
		ReconfigureProvider: cmd.Reconfigure,
		DevContainerImage:   cmd.DevContainerImage,
		DevContainerPath:    cmd.DevContainerPath,
		DevContainerSource:  cmd.DevContainerSource,
		SSHConfigPath:       cmd.SSHConfigPath,
		SSHConfigIncludePath: devsyConfig.ContextOption(
			config.ContextOptionSSHConfigIncludePath,
		),
		Source:         source,
		UID:            cmd.UID,
		ChangeLastUsed: true,
		Owner:          cmd.Owner,
	}
}

func (cmd *UpCmd) prepareSecretsWithProject(
	ctx context.Context,
	devsyConfig *config.Config,
	project *projectSecretContext,
) error {
	if err := mergeEnvFromFiles(&cmd.CLIOptions); err != nil {
		return err
	}

	if cmd.SecretsFile != "" {
		parsed, err := secrets.ParseSecretsFile(cmd.SecretsFile)
		if err != nil {
			return err
		}
		for k, v := range parsed {
			cmd.SecretsEnv = append(cmd.SecretsEnv, k+"="+v)
		}
	}

	if err := cmd.resolveStoredSecrets(ctx, devsyConfig, project); err != nil {
		return err
	}

	if cmd.FeatureSecretsFile == "" {
		cmd.FeatureSecretsFile = os.Getenv("DEVCONTAINER_SECRETS_FILE")
	}
	if cmd.FeatureSecretsFile != "" {
		cmd.CLIOptions.FeatureSecretsFile = cmd.FeatureSecretsFile
	}

	cmd.WorkspaceEnv = options2.InheritFromEnvironment(
		cmd.WorkspaceEnv,
		options2.GitIdentityEnvVars,
		"",
	)

	return nil
}

func (cmd *UpCmd) prepareBootstrapGitToken(
	ctx context.Context,
	devsyConfig *config.Config,
	source *provider2.WorkspaceSource,
) error {
	if cmd.GitTokenSecret == "" {
		return nil
	}
	resolver, err := secrets.NewResolverForConfig(devsyConfig)
	if err != nil {
		return err
	}
	resolved, err := validateBootstrapSecretReference(ctx, resolver, cmd.GitTokenSecret)
	if err != nil {
		return err
	}
	gitToken, err := cmd.buildGitTokenForSource(resolved.Value, source)
	if err != nil {
		return err
	}
	cmd.GitToken = gitToken
	return nil
}

func (cmd *UpCmd) resolveStoredSecrets(
	ctx context.Context,
	devsyConfig *config.Config,
	project *projectSecretContext,
) error {
	requests, err := collectSecretRequests(cmd.Secrets, devsyConfig, project.attachedSecrets())
	if err != nil {
		return err
	}
	if !cmd.hasStoredValues(requests) {
		return nil
	}

	resolver, err := secrets.NewResolverForConfig(devsyConfig)
	if err != nil {
		return err
	}
	if err := project.register(resolver); err != nil {
		return err
	}

	if err := cmd.applyLifecycleSecrets(ctx, requests, resolver); err != nil {
		return err
	}
	if err := cmd.applyEnvVars(ctx, resolver); err != nil {
		return err
	}
	return cmd.applyBuildSecrets(ctx, resolver)
}

func (cmd *UpCmd) hasStoredValues(requests []secretRequest) bool {
	return len(requests) > 0 || len(cmd.EnvVars) > 0 || len(cmd.BuildSecretNames) > 0
}

func (cmd *UpCmd) applyLifecycleSecrets(
	ctx context.Context,
	requests []secretRequest,
	resolver *secrets.Resolver,
) error {
	for _, req := range requests {
		resolved, err := resolver.Resolve(ctx, req.ref)
		if err != nil {
			return err
		}
		if req.mount {
			cmd.SecretsMount = append(cmd.SecretsMount, req.target+"="+resolved.Value)
		} else {
			cmd.SecretsEnv = append(cmd.SecretsEnv, req.target+"="+resolved.Value)
		}
	}
	return nil
}

// applyEnvVars resolves --env entries from the local Devsy store. External
// sensitive sources intentionally use --secret instead: WorkspaceEnv rides in
// the setup argv and is process-list visible.
func (cmd *UpCmd) applyEnvVars(ctx context.Context, resolver *secrets.Resolver) error {
	for _, entry := range cmd.EnvVars {
		name, target, ok := strings.Cut(entry, "=")
		if !ok {
			target = name
		} else if target == "" {
			return fmt.Errorf("invalid --env %q: target after %q= must not be empty", entry, name)
		}
		ref, err := secrets.ParseRef(name)
		if err != nil {
			return err
		}
		if ref.Source != secrets.LocalSourceName {
			return fmt.Errorf(
				"--env only accepts Devsy-managed values; use --secret %s instead",
				name,
			)
		}
		resolved, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return err
		}
		if resolved.Sensitive {
			return fmt.Errorf(
				"%q is a secret and cannot be passed with --env (it would be visible in the process list); use --secret %s instead",
				name,
				name,
			)
		}
		cmd.WorkspaceEnv = append(cmd.WorkspaceEnv, target+"="+resolved.Value)
	}
	return nil
}

func (cmd *UpCmd) applyBuildSecrets(ctx context.Context, resolver *secrets.Resolver) error {
	seen := map[string]string{}
	built := make([]string, 0, len(cmd.BuildSecretNames))
	for _, value := range cmd.BuildSecretNames {
		ref, err := secrets.ParseRef(value)
		if err != nil {
			return err
		}
		if other, dup := seen[ref.Name]; dup {
			return fmt.Errorf(
				"build secrets %q and %q both use BuildKit id %q",
				other,
				ref.String(),
				ref.Name,
			)
		}
		seen[ref.Name] = ref.String()
		resolved, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return err
		}
		built = append(built, ref.Name+"="+resolved.Value)
	}
	cmd.BuildSecrets = built
	return nil
}

func (cmd *UpCmd) buildGitTokenForSource(
	token string,
	source *provider2.WorkspaceSource,
) (*provider2.GitToken, error) {
	host := ""
	if source != nil && source.GitRepository != "" {
		host = gitHostFromSource(source.GitRepository)
	}
	if host == "" {
		host = gitHostFromSource(cmd.Source)
	}
	if host == "" {
		return nil, fmt.Errorf(
			"cannot use --git-token: workspace source %q has no HTTP(S) host to scope the token to",
			cmd.Source,
		)
	}
	username := cmd.GitTokenUsername
	if username == "" {
		username = gitTokenUsernameForHost(host)
	}
	return &provider2.GitToken{Host: host, Username: username, Token: token}, nil
}

// gitHostFromSource returns the host of an HTTP(S) git source, or "". A git
// token is only usable over HTTP(S), so non-HTTP(S) schemes yield "".
func gitHostFromSource(source string) string {
	s := strings.TrimPrefix(source, "git:")
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Host
}

func gitTokenUsernameForHost(host string) string {
	if strings.Contains(host, "gitlab") {
		return "oauth2"
	}
	return "x-access-token"
}

type secretRequest struct {
	ref    secrets.SecretRef
	target string
	mount  bool
}

// collectSecretRequests merges context/project bindings with secret flags;
// explicit flags override automatic bindings for the same reference.
func collectSecretRequests(
	flags []string,
	devsyConfig *config.Config,
	projectSecrets []string,
) ([]secretRequest, error) {
	byName := map[string]secretRequest{}

	var attached []string
	if ctxConfig := devsyConfig.Contexts[devsyConfig.DefaultContext]; ctxConfig != nil {
		attached = ctxConfig.Secrets
	}
	if err := addSecretBindings(byName, attached, "attached"); err != nil {
		return nil, err
	}
	if err := addSecretBindings(byName, projectSecrets, "project"); err != nil {
		return nil, err
	}
	if err := addSecretFlags(byName, flags); err != nil {
		return nil, err
	}

	requests := sortedSecretRequests(byName)
	if err := checkDuplicateMountTargets(requests); err != nil {
		return nil, err
	}
	return requests, nil
}

func addSecretBindings(
	byName map[string]secretRequest,
	values []string,
	kind string,
) error {
	for _, value := range values {
		ref, err := secrets.ParseRef(value)
		if err != nil {
			return fmt.Errorf("invalid %s secret %q: %w", kind, value, err)
		}
		byName[ref.String()] = secretRequest{ref: ref, target: ref.Name}
	}
	return nil
}

func addSecretFlags(byName map[string]secretRequest, values []string) error {
	for _, value := range values {
		req, err := parseSecretFlag(value)
		if err != nil {
			return err
		}
		byName[req.ref.String()] = req
	}
	return nil
}

func sortedSecretRequests(byName map[string]secretRequest) []secretRequest {
	requests := make([]secretRequest, 0, len(byName))
	for _, req := range byName {
		requests = append(requests, req)
	}
	sort.Slice(requests, func(i, j int) bool {
		return requests[i].ref.String() < requests[j].ref.String()
	})
	return requests
}

func checkDuplicateMountTargets(requests []secretRequest) error {
	targets := map[string]string{}
	for _, req := range requests {
		if !req.mount {
			continue
		}
		if other, dup := targets[req.target]; dup {
			return fmt.Errorf(
				"secrets %q and %q both mount to target %q; give one a distinct target=",
				other, req.ref.String(), req.target,
			)
		}
		targets[req.target] = req.ref.String()
	}
	return nil
}

func parseSecretFlag(entry string) (secretRequest, error) {
	parts := strings.Split(entry, ",")
	ref, err := secrets.ParseRef(parts[0])
	if err != nil {
		return secretRequest{}, err
	}
	req := secretRequest{ref: ref}

	for _, opt := range parts[1:] {
		key, value, ok := strings.Cut(opt, "=")
		if !ok {
			return secretRequest{}, fmt.Errorf(
				"invalid secret option %q, expected key=value",
				opt,
			)
		}
		if err := req.applyOption(key, value); err != nil {
			return secretRequest{}, err
		}
	}

	if req.target == "" {
		req.target = req.ref.Name
	}
	return req, nil
}

func (req *secretRequest) applyOption(key, value string) error {
	switch key {
	case "type":
		switch value {
		case "env":
			req.mount = false
		case "mount":
			req.mount = true
		default:
			return fmt.Errorf("invalid secret type %q, expected env or mount", value)
		}
	case "target":
		if value == "" {
			return fmt.Errorf("secret option target must not be empty")
		}
		req.target = value
	default:
		return fmt.Errorf("invalid secret option %q, expected type or target", key)
	}
	return nil
}

func (cmd *UpCmd) parseWorkspaceSource() (*provider2.WorkspaceSource, error) {
	source, err := cmd.resolveExplicitSource()
	if err != nil {
		return nil, err
	}
	if source != nil {
		return source, nil
	}

	if cmd.Source == "" {
		return nil, nil
	}

	source = provider2.ParseWorkspaceSource(cmd.Source)
	if source == nil {
		return nil, fmt.Errorf("workspace source is missing")
	}
	if source.LocalFolder != "" && cmd.Platform.Enabled {
		return nil, fmt.Errorf("local folder is not supported in platform mode. " +
			"Specify a Git repository instead")
	}

	return source, nil
}

func (cmd *UpCmd) resolveExplicitSource() (*provider2.WorkspaceSource, error) {
	if cmd.FromSnapshot == "" {
		return nil, nil
	}
	sourceStr, devContainerSource, err := snapshotpkg.RestoreComposition(cmd.FromSnapshot)
	if err != nil {
		return nil, fmt.Errorf("parse --from-snapshot ref: %w", err)
	}
	cmd.DevContainerSource = devContainerSource

	if cmd.ID == "" {
		ref, err := snapshotpkg.ParseRef(cmd.FromSnapshot)
		if err != nil {
			return nil, fmt.Errorf("parse --from-snapshot ref: %w", err)
		}
		cmd.ID = ref.WorkspaceID
	}

	source := provider2.ParseWorkspaceSource(sourceStr)
	if source == nil {
		return nil, fmt.Errorf(
			"compose workspace source from --from-snapshot: unexpected source %q",
			sourceStr,
		)
	}
	return source, nil
}

func (cmd *UpCmd) validateFromSnapshot(ctx context.Context, args []string) error {
	if cmd.FromSnapshot == "" {
		return nil
	}
	if cmd.Source != "" || (len(args) > 0 && args[0] != "") {
		return fmt.Errorf("cannot combine --from-snapshot with an explicit source")
	}
	if cmd.Platform.Enabled {
		return fmt.Errorf("--from-snapshot is not supported in platform mode")
	}
	manifest, err := snapshotpkg.PullManifest(ctx, cmd.FromSnapshot)
	if err != nil {
		return fmt.Errorf("validate --from-snapshot ref: %w", err)
	}
	return cmd.applyFromSnapshotOverrides(manifest)
}

func (cmd *UpCmd) applyFromSnapshotOverrides(manifest *snapshotpkg.Manifest) error {
	runArgs, err := manifest.RunArgs()
	if err != nil {
		return fmt.Errorf("read --from-snapshot run args: %w", err)
	}
	cmd.RunArgs = runArgs

	containerEnv, err := manifest.ContainerEnv()
	if err != nil {
		return fmt.Errorf("read --from-snapshot container env: %w", err)
	}
	cmd.ContainerEnv = containerEnv

	if cmd.RemoteUser == "" {
		cmd.RemoteUser = manifest.RemoteUser()
	}
	return nil
}

func (cmd *UpCmd) resolveSSHConfig(devsyConfig *config.Config) {
	if cmd.SSHConfigPath == "" {
		cmd.SSHConfigPath = devsyConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}
}

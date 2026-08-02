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
	if err := mergeDevsyUpOptions(&cmd.CLIOptions); err != nil {
		return nil, err
	}
	if cmd.Platform.Enabled {
		log.Debug("Running in platform mode")
		log.Debug("Using error output stream")
		config.MergeContextOptions(devsyConfig.Current(), os.Environ())
	}
	if err := cmd.prepareSecrets(devsyConfig); err != nil {
		return nil, err
	}
	if err := cmd.validateFromSnapshot(ctx, args); err != nil {
		return nil, err
	}
	source, err := cmd.parseWorkspaceSource()
	if err != nil {
		return nil, err
	}
	cmd.resolveSSHConfig(devsyConfig)

	log.Debugf("up: resolving workspace with cmd.IDE=%q ide-launch=%q", cmd.IDE, cmd.IDELaunch)
	client, err := workspace2.Resolve(
		ctx,
		devsyConfig,
		cmd.resolveParams(args, source, devsyConfig),
	)
	if err != nil {
		return nil, err
	}
	if !cmd.Platform.Enabled {
		proInstance := workspace2.GetProInstance(devsyConfig, client.Provider())
		if err := workspace2.CheckProviderUpdate(ctx, devsyConfig, proInstance); err != nil {
			return nil, err
		}
	}
	return client, nil
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

func (cmd *UpCmd) prepareSecrets(devsyConfig *config.Config) error {
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

	if err := cmd.resolveStoredSecrets(devsyConfig); err != nil {
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

func (cmd *UpCmd) resolveStoredSecrets(devsyConfig *config.Config) error {
	requests, err := collectSecretRequests(cmd.Secrets, devsyConfig)
	if err != nil {
		return err
	}
	if !cmd.hasStoredValues(requests) {
		return nil
	}

	store, err := secrets.NewStoreForConfig(devsyConfig)
	if err != nil {
		return err
	}
	r := secretResolver{store: store, context: devsyConfig.DefaultContext}

	if err := cmd.applyLifecycleSecrets(requests, r.get); err != nil {
		return err
	}
	if err := cmd.applyEnvVars(r.get, r.sensitive); err != nil {
		return err
	}
	if err := cmd.applyBuildSecrets(r.get); err != nil {
		return err
	}
	return cmd.applyGitToken(r.get)
}

type secretResolver struct {
	store   secrets.Store
	context string
}

func (r secretResolver) get(name string) (string, error) {
	value, err := r.store.Get(r.context, name)
	if err != nil {
		return "", fmt.Errorf("resolve secret %q in context %q: %w", name, r.context, err)
	}
	return value, nil
}

func (r secretResolver) sensitive(name string) (bool, error) {
	meta, err := r.store.Meta(r.context, name)
	if err != nil {
		return false, fmt.Errorf("resolve secret %q in context %q: %w", name, r.context, err)
	}
	return meta.Sensitive(), nil
}

func (cmd *UpCmd) hasStoredValues(requests []secretRequest) bool {
	return len(requests) > 0 || len(cmd.EnvVars) > 0 ||
		len(cmd.BuildSecretNames) > 0 || cmd.GitTokenSecret != ""
}

func (cmd *UpCmd) applyLifecycleSecrets(
	requests []secretRequest,
	get func(string) (string, error),
) error {
	for _, req := range requests {
		value, err := get(req.name)
		if err != nil {
			return err
		}
		if req.mount {
			cmd.SecretsMount = append(cmd.SecretsMount, req.target+"="+value)
		} else {
			cmd.SecretsEnv = append(cmd.SecretsEnv, req.target+"="+value)
		}
	}
	return nil
}

// applyEnvVars resolves --env entries into WorkspaceEnv. A sensitive secret must
// never be routed here: WorkspaceEnv rides in the setup argv (ps-visible).
func (cmd *UpCmd) applyEnvVars(
	get func(string) (string, error),
	isSensitive func(string) (bool, error),
) error {
	for _, entry := range cmd.EnvVars {
		name, target, ok := strings.Cut(entry, "=")
		if !ok {
			target = name
		} else if target == "" {
			return fmt.Errorf("invalid --env %q: target after %q= must not be empty", entry, name)
		}
		sensitive, err := isSensitive(name)
		if err != nil {
			return err
		}
		if sensitive {
			return fmt.Errorf(
				"%q is a secret and cannot be passed with --env (it would be visible in the process list); use --secret %s instead",
				name,
				name,
			)
		}
		value, err := get(name)
		if err != nil {
			return err
		}
		cmd.WorkspaceEnv = append(cmd.WorkspaceEnv, target+"="+value)
	}
	return nil
}

func (cmd *UpCmd) applyBuildSecrets(get func(string) (string, error)) error {
	for _, name := range cmd.BuildSecretNames {
		value, err := get(name)
		if err != nil {
			return err
		}
		cmd.BuildSecrets = append(cmd.BuildSecrets, name+"="+value)
	}
	return nil
}

func (cmd *UpCmd) applyGitToken(get func(string) (string, error)) error {
	if cmd.GitTokenSecret == "" {
		return nil
	}
	token, err := get(cmd.GitTokenSecret)
	if err != nil {
		return err
	}
	gitToken, err := cmd.buildGitToken(token)
	if err != nil {
		return err
	}
	cmd.GitToken = gitToken
	return nil
}

// buildGitToken host-scopes the token; it fails on an unresolvable host so the
// token is never left unscoped.
func (cmd *UpCmd) buildGitToken(token string) (*provider2.GitToken, error) {
	host := gitHostFromSource(cmd.Source)
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
// token is only usable over HTTP(S), so non-HTTP(S) schemes (e.g. ssh) yield "".
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
	name   string
	target string
	mount  bool
}

// collectSecretRequests merges context bindings with secret flags; flags override bindings.
func collectSecretRequests(flags []string, devsyConfig *config.Config) ([]secretRequest, error) {
	byName := map[string]secretRequest{}

	if ctxConfig := devsyConfig.Contexts[devsyConfig.DefaultContext]; ctxConfig != nil {
		for _, name := range ctxConfig.Secrets {
			byName[name] = secretRequest{name: name, target: name}
		}
	}

	for _, entry := range flags {
		req, err := parseSecretFlag(entry)
		if err != nil {
			return nil, err
		}
		byName[req.name] = req
	}

	requests := make([]secretRequest, 0, len(byName))
	for _, req := range byName {
		requests = append(requests, req)
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].name < requests[j].name })

	if err := checkDuplicateMountTargets(requests); err != nil {
		return nil, err
	}

	return requests, nil
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
				other, req.name, req.target,
			)
		}
		targets[req.target] = req.name
	}
	return nil
}

func parseSecretFlag(entry string) (secretRequest, error) {
	parts := strings.Split(entry, ",")
	req := secretRequest{name: parts[0]}
	if req.name == "" {
		return secretRequest{}, fmt.Errorf("invalid secret %q: missing name", entry)
	}

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
		req.target = req.name
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

// resolveExplicitSource returns an explicit WorkspaceSource when --from-snapshot
// is set, composed identically to `devsy snapshot restore` via
// snapshot.RestoreComposition ("snapshot:<ref>" source, "image:<repo>:<tag>-fs"
// DevContainerSource), taking priority over positional-arg source resolution.
// It also sets cmd.DevContainerSource so the workspace runs the snapshot's
// committed filesystem image instead of rebuilding, matching restore's
// behavior exactly.
//
// Since --from-snapshot forbids a positional source (validateFromSnapshot),
// there is no other way for the workspace ID to reach ResolveParams.DesiredID
// on this path; without defaulting it here, workspace resolution falls back
// to selecting an unrelated existing workspace (or fails confusingly in
// non-TTY contexts). So when --id wasn't given explicitly, default it from
// the snapshot ref's workspace id, mirroring `devsy snapshot restore`'s
// buildWorkspace.
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

// validateFromSnapshot enforces --from-snapshot's invariants before workspace
// resolution: it cannot be combined with an explicit source (positional arg
// or --source) or used in platform mode (snapshots are local-only — `devsy
// snapshot create` rejects machine-provider workspaces up front, and restore
// has no remote-registry-backed equivalent of a platform-managed container),
// and the referenced snapshot must actually exist. Mirrors `devsy snapshot
// restore`'s upfront PullManifest check so a bad or missing ref fails fast
// with a clear error instead of partway through workspace creation.
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
	runArgs, err := manifest.RunArgs()
	if err != nil {
		return fmt.Errorf("read --from-snapshot run args: %w", err)
	}
	cmd.RunArgs = runArgs
	return nil
}

func (cmd *UpCmd) resolveSSHConfig(devsyConfig *config.Config) {
	if cmd.SSHConfigPath == "" {
		cmd.SSHConfigPath = devsyConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}
}

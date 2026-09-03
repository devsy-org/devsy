package up

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"

	gitpkg "github.com/devsy-org/devsy/pkg/git"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
)

type projectSecretContext struct {
	config  *secrets.ProjectConfig
	sources map[string]secrets.Source
}

func (p *projectSecretContext) attachedSecrets() []string {
	if p == nil || p.config == nil {
		return nil
	}
	return p.config.Secrets
}

func (p *projectSecretContext) register(resolver *secrets.Resolver) error {
	if p == nil {
		return nil
	}
	for _, sourceConfig := range p.config.SecretSources {
		source := p.sources[sourceConfig.Name]
		if source == nil {
			return fmt.Errorf("project secret source %q was not loaded", sourceConfig.Name)
		}
		if err := resolver.Register(sourceConfig.Name, sourceConfig.Type, source); err != nil {
			return fmt.Errorf("register project secret source %q: %w", sourceConfig.Name, err)
		}
	}
	return nil
}

func (cmd *UpCmd) discoverProjectSecrets(
	ctx context.Context,
	source *provider2.WorkspaceSource,
) (*projectSecretContext, error) {
	if source == nil {
		return nil, nil
	}
	if source.LocalFolder != "" {
		return discoverLocalProjectSecrets(source.LocalFolder)
	}
	if source.GitRepository == "" {
		return nil, nil
	}
	return cmd.discoverRemoteProjectSecrets(ctx, source)
}

func discoverLocalProjectSecrets(root string) (*projectSecretContext, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg, found, err := secrets.LoadProjectConfigFromRoot(root)
	if err != nil || !found {
		return nil, err
	}
	project := &projectSecretContext{config: cfg, sources: map[string]secrets.Source{}}
	for _, sourceConfig := range cfg.SecretSources {
		resolvedPath, err := secrets.ResolveProjectSourcePath(root, sourceConfig.Path)
		if err != nil {
			return nil, fmt.Errorf("load project secret source %q: %w", sourceConfig.Name, err)
		}
		project.sources[sourceConfig.Name] = secrets.NewSOPSSource(
			sourceConfig.Name,
			resolvedPath,
			sourceConfig.Format,
		)
	}
	return project, nil
}

func (cmd *UpCmd) discoverRemoteProjectSecrets(
	ctx context.Context,
	source *provider2.WorkspaceSource,
) (*projectSecretContext, error) {
	info := &gitpkg.GitInfo{
		Repository: source.GitRepository,
		Branch:     source.GitBranch,
		Commit:     source.GitCommit,
		PR:         source.GitPRReference,
		SubPath:    source.GitSubPath,
	}
	inspection, err := gitpkg.InspectRemote(ctx, info, cmd.gitInspectionEnv())
	if err != nil {
		return nil, err
	}
	defer func() { _ = inspection.Close() }()

	configBytes, err := inspection.ReadFile(ctx, secrets.ProjectConfigPath)
	if err != nil {
		if errors.Is(err, gitpkg.ErrRevisionPathNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("discover repository secret configuration: %w", err)
	}
	cfg, err := secrets.ParseProjectConfig(configBytes)
	if err != nil {
		return nil, err
	}
	project := &projectSecretContext{config: cfg, sources: map[string]secrets.Source{}}
	for _, sourceConfig := range cfg.SecretSources {
		cleanPath, err := secrets.CleanProjectSourcePath(sourceConfig.Path)
		if err != nil {
			return nil, fmt.Errorf("load project secret source %q: %w", sourceConfig.Name, err)
		}
		encrypted, err := inspection.ReadFile(ctx, cleanPath)
		if err != nil {
			return nil, fmt.Errorf(
				"load project secret source %q at %q: %w",
				sourceConfig.Name,
				cleanPath,
				err,
			)
		}
		project.sources[sourceConfig.Name] = secrets.NewSOPSDataSource(
			sourceConfig.Name,
			cleanPath,
			sourceConfig.Format,
			encrypted,
		)
	}
	return project, nil
}

func (cmd *UpCmd) gitInspectionEnv() []string {
	env := gitpkg.GetDefaultExtraEnv(cmd.StrictHostKeyChecking)
	if cmd.GitToken == nil || cmd.GitToken.Token == "" {
		return env
	}
	host := cmd.GitToken.Host
	if host == "" {
		return env
	}
	username := cmd.GitToken.Username
	if username == "" {
		username = gitTokenUsernameForHost(host)
	}
	credential := base64.StdEncoding.EncodeToString([]byte(username + ":" + cmd.GitToken.Token))
	key := "http.https://" + host + "/.extraHeader"
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0="+key,
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+credential,
	)
}

func validateBootstrapSecretReference(
	ctx context.Context,
	resolver *secrets.Resolver,
	value string,
) (secrets.ResolvedSecret, error) {
	ref, err := secrets.ParseRef(value)
	if err != nil {
		return secrets.ResolvedSecret{}, err
	}
	resolved, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return secrets.ResolvedSecret{}, fmt.Errorf(
			"cannot resolve bootstrap secret %q before repository acquisition: %w",
			value,
			err,
		)
	}
	return resolved, nil
}

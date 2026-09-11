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
		folder := source.LocalFolder
		if source.GitSubPath != "" {
			cleanSubPath, err := secrets.CleanProjectSourcePath(source.GitSubPath)
			if err != nil {
				return nil, fmt.Errorf("invalid subpath %q: %w", source.GitSubPath, err)
			}
			folder = filepath.Join(folder, filepath.FromSlash(cleanSubPath))
		}
		return cmd.discoverLocalProjectSecrets(folder)
	}
	if source.GitRepository == "" {
		return nil, nil
	}
	return cmd.discoverRemoteProjectSecrets(ctx, source)
}

func (cmd *UpCmd) discoverLocalProjectSecrets(root string) (*projectSecretContext, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg, found, err := secrets.LoadProjectConfigFromRootWithOptions(
		root,
		cmd.DevContainerPath,
		cmd.DevContainerID,
	)
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
	if inspection.Revision() != "" {
		source.GitCommit = inspection.Revision()
	}

	cfg, err := cmd.readRemoteProjectConfig(ctx, inspection)
	if err != nil || cfg == nil {
		return nil, err
	}

	sources, err := loadRemoteProjectSources(ctx, inspection, cfg)
	if err != nil {
		return nil, err
	}
	return &projectSecretContext{config: cfg, sources: sources}, nil
}

func (cmd *UpCmd) readRemoteProjectConfig(
	ctx context.Context,
	inspection *gitpkg.Inspection,
) (*secrets.ProjectConfig, error) {
	devContainerBytes, _, err := inspection.ReadDevContainerConfig(
		ctx,
		cmd.DevContainerPath,
		cmd.DevContainerID,
	)
	if err == nil {
		cfg, parseErr := parseValidProjectConfig(devContainerBytes)
		if parseErr != nil {
			return nil, fmt.Errorf("parse devcontainer.json: %w", parseErr)
		}
		return cfg, nil
	}
	if !errors.Is(err, gitpkg.ErrRevisionPathNotFound) {
		return nil, fmt.Errorf("discover repository devcontainer configuration: %w", err)
	}
	return nil, nil
}

func parseValidProjectConfig(data []byte) (*secrets.ProjectConfig, error) {
	parsed, err := secrets.ParseProjectConfig(data)
	if err != nil {
		return nil, err
	}
	if parsed != nil && (len(parsed.SecretSources) > 0 || len(parsed.Secrets) > 0) {
		return parsed, nil
	}
	return nil, nil
}

func loadRemoteProjectSources(
	ctx context.Context,
	inspection *gitpkg.Inspection,
	cfg *secrets.ProjectConfig,
) (map[string]secrets.Source, error) {
	sources := make(map[string]secrets.Source, len(cfg.SecretSources))
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
		sources[sourceConfig.Name] = secrets.NewSOPSDataSource(
			sourceConfig.Name,
			cleanPath,
			sourceConfig.Format,
			encrypted,
		)
	}
	return sources, nil
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
	return gitpkg.AppendGitConfig(env, key, "Authorization: Basic "+credential)
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

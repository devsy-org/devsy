package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/moby/patternmatcher/ignorefile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var extraSearchLocations = []string{
	"/home/devsy/" + config.ConfigDirName + "/agent",
	"/opt/devsy/agent",
	"/var/lib/devsy/agent",
	config.ContainerDataDir + "/agent",
}

var ErrFindAgentHomeDir = errors.New("could not find devsy home directory")

func GetAgentDaemonLogDir(agentDir string) (string, error) {
	if IsHostAgentInvocation(agentDir) {
		return "", errors.New(
			"agent daemon log directory is only available inside the workspace container or machine",
		)
	}
	return FindAgentHomeDir(agentDir)
}

func findDir(agentDir string, validate func(path string) bool) string {
	if agentDir != "" {
		if validate(agentDir) {
			return agentDir
		}
		return ""
	}

	if home := os.Getenv(config.EnvHome); home != "" {
		agentDir := filepath.Join(home, "agent")
		if validate(agentDir) {
			return agentDir
		}
		return ""
	}

	for _, dir := range candidateAgentDirs() {
		if validate(dir) {
			return dir
		}
	}
	return ""
}

func candidateAgentDirs() []string {
	var dirs []string
	if home, _ := util.UserHomeDir(); home != "" {
		dirs = append(dirs, filepath.Join(home, config.ConfigDirName, "agent"))
	}
	if root, _ := command.GetHome("root"); root != "" {
		dirs = append(dirs, filepath.Join(root, config.ConfigDirName, "agent"))
	}
	if execPath, _ := os.Executable(); execPath != "" {
		dirs = append(dirs, filepath.Join(filepath.Dir(execPath), "agent"))
	}
	return append(dirs, extraSearchLocations...)
}

func FindAgentHomeDir(agentDir string) (string, error) {
	homeDir := findDir(agentDir, isDevsyHome)
	if homeDir != "" {
		return homeDir, nil
	}

	return "", ErrFindAgentHomeDir
}

func isDevsyHome(dir string) bool {
	// #nosec G703 -- read-only existence probe of a derived agent path.
	_, err := os.Stat(filepath.Join(dir, "contexts"))
	return err == nil
}

func PrepareAgentHomeDir(agentDir string) (string, error) {
	homeDir, err := FindAgentHomeDir(agentDir)
	if err == nil {
		return homeDir, nil
	}

	execDir := findDir(agentDir, func(path string) bool {
		ok, _ := isDirExecutable(path)
		return ok
	})
	if execDir != "" {
		return execDir, nil
	}

	if agentDir != "" {
		_, err := isDirExecutable(agentDir)
		return "", err
	}

	return "", errors.New("could not find an executable directory")
}

func isDirExecutable(dir string) (bool, error) {
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return false, err
		}
		dir = abs
	}

	// #nosec G301,G703 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return dirAllowsExec(dir)
}

func GetAgentWorkspaceContentDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, "content")
}

func GetAgentBinariesDirFromWorkspaceDir(workspaceDir string) (string, error) {
	_, err := os.Stat(workspaceDir)
	if err == nil {
		return filepath.Join(workspaceDir, "binaries"), nil
	}

	return "", os.ErrNotExist
}

var containerDetector = isLikelyContainer

func IsHostAgentInvocation(agentDir string) bool {
	if agentDir != "" {
		return false
	}
	return !containerDetector()
}

func GetAgentBinariesDir(agentDir, context, workspaceID string) (string, error) {
	if context == "" {
		context = config.DefaultContext
	}
	if IsHostAgentInvocation(agentDir) {
		workspaceDir, err := provider2.GetWorkspaceAgentDir(context, workspaceID)
		if err != nil {
			return "", err
		}
		return GetAgentBinariesDirFromWorkspaceDir(workspaceDir)
	}

	homeDir, err := FindAgentHomeDir(agentDir)
	if err != nil {
		return "", err
	}

	workspaceDir := filepath.Join(homeDir, "contexts", context, "workspaces", workspaceID)

	return GetAgentBinariesDirFromWorkspaceDir(workspaceDir)
}

func GetAgentWorkspaceDir(agentDir, context, workspaceID string) (string, error) {
	if context == "" {
		context = config.DefaultContext
	}
	if IsHostAgentInvocation(agentDir) {
		workspaceDir, err := provider2.GetWorkspaceAgentDir(context, workspaceID)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(workspaceDir); statErr == nil {
			return workspaceDir, nil
		}
		return "", os.ErrNotExist
	}

	homeDir, err := FindAgentHomeDir(agentDir)
	if err != nil {
		return "", err
	}

	workspaceDir := filepath.Join(homeDir, "contexts", context, "workspaces", workspaceID)

	_, err = os.Stat(workspaceDir)
	if err == nil {
		return workspaceDir, nil
	}

	return "", os.ErrNotExist
}

func CreateAgentWorkspaceDir(agentDir, context, workspaceID string) (string, error) {
	if context == "" {
		context = config.DefaultContext
	}
	if IsHostAgentInvocation(agentDir) {
		workspaceDir, err := provider2.GetWorkspaceAgentDir(context, workspaceID)
		if err != nil {
			return "", err
		}
		// #nosec G301 -- mirrors the legacy 0o755 perms below.
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return "", err
		}
		return workspaceDir, nil
	}

	homeDir, err := PrepareAgentHomeDir(agentDir)
	if err != nil {
		return "", err
	}

	workspaceDir := filepath.Join(homeDir, "contexts", context, "workspaces", workspaceID)

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(workspaceDir, 0o755)
	if err != nil {
		return "", err
	}

	return workspaceDir, nil
}

type CloneWorkspaceParams struct {
	Source           *provider2.WorkspaceSource
	AgentConfig      *provider2.ProviderAgentConfig
	WorkspaceDir     string
	Helper           string
	Options          provider2.CLIOptions
	OverwriteContent bool
}

func CloneRepositoryForWorkspace(ctx context.Context, p CloneWorkspaceParams) error {
	logCloneSource(p.Source)
	defer removeGitCredentialHelper(ctx, p.Helper, p.WorkspaceDir)

	if err := ensureGit(ctx, p.AgentConfig); err != nil {
		return err
	}

	if p.OverwriteContent {
		if err := removeDirContents(p.WorkspaceDir); err != nil {
			return fmt.Errorf("cleanup workspace: %w", err)
		}
	}

	extraEnv, cleanupSSH, err := setupGitSSH(p.Options, p.AgentConfig)
	if err != nil {
		return err
	}
	defer cleanupSSH()

	if err := cloneRepository(ctx, p, extraEnv); err != nil {
		return err
	}

	log.Info("cloned repository")
	return applyDevsyIgnore(p.WorkspaceDir)
}

func logCloneSource(source *provider2.WorkspaceSource) {
	log.Info("cloning repository")
	log.Infof("URL: %s", source.GitRepository)
	if source.GitBranch != "" {
		log.Infof("branch: %s", source.GitBranch)
	}
	if source.GitCommit != "" {
		log.Infof("commit: %s", source.GitCommit)
	}
	if source.GitSubPath != "" {
		log.Infof("subpath: %s", source.GitSubPath)
	}
	if source.GitPRReference != "" {
		log.Infof("PR: %s", source.GitPRReference)
	}
}

func removeGitCredentialHelper(ctx context.Context, helper, workspaceDir string) {
	if helper == "" {
		return
	}
	gitConfigPath := gitcredentials.GetLocalGitConfigPath(workspaceDir)
	if _, err := os.Stat(gitConfigPath); err != nil {
		// Nothing to clean up, e.g. the clone failed and the workspace dir was
		// already removed.
		return
	}
	if err := gitcredentials.RemoveHelperFromPath(ctx, gitConfigPath); err != nil {
		log.Errorf("remove git credential helper: %v", err)
	}
}

func ensureGit(ctx context.Context, agentConfig *provider2.ProviderAgentConfig) error {
	if command.Exists("git") {
		return nil
	}
	if local, _ := agentConfig.Local.Bool(); local {
		return errors.New("git not installed: install git and add it to PATH")
	}
	return git.InstallBinary(ctx)
}

func setupGitSSH(
	options provider2.CLIOptions,
	agentConfig *provider2.ProviderAgentConfig,
) ([]string, func(), error) {
	noop := func() {}
	credentials := slices.Concat(
		options.Platform.UserCredentials.GitSsh,
		options.Platform.ProjectCredentials.GitSsh,
	)
	if len(credentials) == 0 {
		return nil, noop, nil
	}

	keys := make([]string, 0, len(credentials))
	for _, key := range credentials {
		keys = append(keys, key.Key)
	}

	extraEnv, cleanup, err := setupSSHKey(keys, agentConfig.Path)
	if err != nil {
		return nil, noop, err
	}
	return extraEnv, cleanup, nil
}

func cloneRepository(ctx context.Context, p CloneWorkspaceParams, extraEnv []string) error {
	if usePlatformGitcache(p.Options) {
		return cloneViaPlatformGitcache(ctx, p, extraEnv)
	}
	return cloneViaGit(ctx, p, extraEnv)
}

func usePlatformGitcache(options provider2.CLIOptions) bool {
	if !options.Platform.Enabled || options.Platform.RunnerSocket == "" {
		return false
	}
	_, err := os.Stat(options.Platform.RunnerSocket)
	return err == nil
}

func cloneViaPlatformGitcache(
	ctx context.Context,
	p CloneWorkspaceParams,
	extraEnv []string,
) error {
	grpcClient, err := grpc.NewClient(
		"unix://"+p.Options.Platform.RunnerSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithIdleTimeout(180*time.Minute),
	)
	if err != nil {
		return fmt.Errorf("create platform gitcache client: %w", err)
	}
	defer func() { _ = grpcClient.Close() }()

	jsonOptions, err := json.Marshal(&devsy.CloneOptions{
		Repository:        p.Source.GitRepository,
		Branch:            p.Source.GitBranch,
		Commit:            p.Source.GitCommit,
		PRReference:       p.Source.GitPRReference,
		SubPath:           p.Source.GitSubPath,
		CredentialsHelper: p.Helper,
		ExtraEnv: append(
			git.GetDefaultExtraEnv(p.Options.StrictHostKeyChecking),
			extraEnv...),
	})
	if err != nil {
		return fmt.Errorf("marshal git options: %w", err)
	}

	log.Infof("cloning repository %s via platform gitcache", p.Source.GitRepository)
	_, err = devsy.NewRunnerClient(grpcClient).Clone(ctx, &devsy.CloneRequest{
		TargetPath: p.WorkspaceDir,
		Options:    string(jsonOptions),
	})
	if err != nil {
		return failedClone(p.WorkspaceDir, "clone repository (with gitcache)", err)
	}
	return nil
}

func cloneViaGit(ctx context.Context, p CloneWorkspaceParams, extraEnv []string) error {
	if p.Options.Platform.GitCloneStrategy != "" {
		log.Infof("using %s clone strategy", p.Options.Platform.GitCloneStrategy)
	}
	if p.Options.Platform.GitSkipLFS {
		log.Info("skipping Git LFS")
	}

	gitInfo := &git.GitInfo{
		Repository: p.Source.GitRepository,
		Branch:     p.Source.GitBranch,
		Commit:     p.Source.GitCommit,
		PR:         p.Source.GitPRReference,
		SubPath:    p.Source.GitSubPath,
	}
	repo := git.At(p.WorkspaceDir,
		git.WithStrictHostKeyChecking(p.Options.StrictHostKeyChecking),
		git.WithEnv(extraEnv))
	if err := repo.CloneFromInfo(ctx, gitInfo, p.Helper, getGitOptions(p.Options)...); err != nil {
		return failedClone(p.WorkspaceDir, "clone repository", err)
	}
	return nil
}

func failedClone(workspaceDir, label string, cloneErr error) error {
	if cleanupErr := cleanupWorkspaceDir(workspaceDir); cleanupErr != nil {
		return fmt.Errorf("%s: %w, cleanup workspace: %w", label, cloneErr, cleanupErr)
	}
	return fmt.Errorf("%s: %w", label, cloneErr)
}

func applyDevsyIgnore(workspaceDir string) error {
	f, err := os.Open(
		filepath.Join(workspaceDir, config.IgnoreFileName),
	) // #nosec G304 -- path is controlled by the application, not user input
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		log.Warnf("%s is invalid: %v", config.IgnoreFileName, err)
		return nil
	}
	for _, exclude := range excludes {
		_ = os.RemoveAll(filepath.Join(workspaceDir, exclude))
	}
	log.Debugf("ignoring files from %s: %v", config.IgnoreFileName, excludes)
	return nil
}

func getGitOptions(options provider2.CLIOptions) []git.Option {
	var gitOpts []git.Option
	if options.GitCloneStrategy != "" {
		gitOpts = append(gitOpts, git.WithCloneStrategy(options.GitCloneStrategy))
	}
	if options.Platform.GitCloneStrategy != "" {
		gitOpts = append(
			gitOpts,
			git.WithCloneStrategy(git.CloneStrategy(options.Platform.GitCloneStrategy)),
		)
	}
	if options.Platform.GitSkipLFS {
		gitOpts = append(gitOpts, git.WithLFSMode(git.LFSSkip))
	} else {
		gitOpts = append(gitOpts, git.WithLFSMode(options.GitLFSMode))
	}
	if options.GitCloneRecursiveSubmodules {
		gitOpts = append(gitOpts, git.WithRecursiveSubmodules())
	}
	return gitOpts
}

func cleanupWorkspaceDir(workspaceDir string) error {
	return os.RemoveAll(workspaceDir)
}

func setupSSHKey(keys []string, agentPath string) ([]string, func(), error) {
	keyFiles := make([]string, 0, len(keys))
	cleanup := func() {
		for _, keyFile := range keyFiles {
			_ = os.Remove(keyFile)
		}
	}

	for _, key := range keys {
		keyFile, err := writeTempSSHKey(key)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		keyFiles = append(keyFiles, keyFile)
	}

	gitSSHCmd := []string{agentPath, "internal", "ssh-git-clone"}
	for _, keyFile := range keyFiles {
		gitSSHCmd = append(gitSSHCmd, "--key-file="+keyFile)
	}
	env := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=" + command.Quote(gitSSHCmd),
	}
	return env, cleanup, nil
}

func writeTempSSHKey(key string) (string, error) {
	keyFile, err := os.CreateTemp("", "")
	if err != nil {
		return "", err
	}
	defer func() { _ = keyFile.Close() }()

	if err := writeSSHKey(keyFile, key); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", err
	}
	if err := os.Chmod(keyFile.Name(), 0o400); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", err
	}
	return keyFile.Name(), nil
}

func writeSSHKey(key *os.File, sshKey string) error {
	data, err := base64.StdEncoding.DecodeString(sshKey)
	if err != nil {
		return err
	}

	_, err = key.Write(data)
	return err
}

func removeDirContents(dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		entryPath := filepath.Join(dirPath, entry.Name())
		if entry.IsDir() {
			err = os.RemoveAll(entryPath)
		} else {
			err = os.Remove(entryPath)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

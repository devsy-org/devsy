package setup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/command"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	copy2 "github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/sharedfile"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// DotfilesConfig holds the parameters needed to install dotfiles inside
// the container as part of the lifecycle.
type DotfilesConfig struct {
	Repository    string
	InstallScript string
	RemoteUser    string
}

type ContainerSetupConfig struct {
	SetupInfo         *config.Result
	ExtraWorkspaceEnv []string
	SecretsEnv        []string
	SecretsMount      []string
	ChownProjects     bool
	Prebuild          bool
	PlatformOptions   *devsy.PlatformOptions
	TunnelClient      tunnel.TunnelClient
	Dotfiles          DotfilesConfig
	SkipPostCreate    bool
	SkipPostStart     bool
	SkipPostAttach    bool
	WaitFor           LifecyclePhase
}

// SetupContainerPreAttach runs container setup up to and including the waitFor
// lifecycle phase. Hooks after waitFor are returned as DeferredHooks for the
// caller to launch in the background.
func SetupContainerPreAttach(
	ctx context.Context,
	cfg *ContainerSetupConfig,
) (DeferredHooks, error) {
	if err := validateContainerSetupConfig(cfg); err != nil {
		return DeferredHooks{}, err
	}

	writeResultFile(cfg)

	if err := setupWorkspaceOwnership(cfg); err != nil {
		return DeferredHooks{}, err
	}

	if err := setupEnvironment(cfg); err != nil {
		return DeferredHooks{}, err
	}

	if err := writeSecretFiles(cfg); err != nil {
		return DeferredHooks{}, err
	}

	setupOptionalFeatures(ctx, cfg)

	log.Debugf("running pre-attach lifecycle hooks")
	deferred, err := RunPreAttachHooks(ctx, cfg.SetupInfo, PreAttachOptions{
		Prebuild:     cfg.Prebuild,
		Dotfiles:     cfg.Dotfiles,
		SecretsEnv:   cfg.SecretsEnv,
		SecretsMount: cfg.SecretsMount,
		Skip: SkipPhases{
			PostCreate: cfg.SkipPostCreate,
			PostStart:  cfg.SkipPostStart,
			PostAttach: cfg.SkipPostAttach,
		},
		WaitFor: cfg.WaitFor,
	})
	if err != nil {
		return DeferredHooks{}, fmt.Errorf("lifecycle hooks pre-attach: %w", err)
	}

	log.Debugf("pre-attach setup completed")
	return deferred, nil
}

// SetupContainerPostAttach runs postAttachCommand only.
// Called after the IDE has been opened.
func SetupContainerPostAttach(ctx context.Context, cfg *ContainerSetupConfig) error {
	log.Debugf("running post-attach lifecycle hooks")
	if err := RunPostAttachHooks(
		ctx,
		cfg.SetupInfo,
		cfg.SecretsEnv,
		cfg.SecretsMount,
		cfg.SkipPostAttach,
	); err != nil {
		return fmt.Errorf("lifecycle hooks post-attach: %w", err)
	}

	log.Debugf("devcontainer setup completed")
	return nil
}

func validateContainerSetupConfig(cfg *ContainerSetupConfig) error {
	if cfg == nil {
		return fmt.Errorf("container setup config is nil")
	}
	if cfg.SetupInfo == nil {
		return fmt.Errorf("setup info not found in container setup config")
	}
	if cfg.SetupInfo.MergedConfig == nil {
		return fmt.Errorf("merged devcontainer config not found in container setup config")
	}

	return nil
}

func writeSecretFiles(cfg *ContainerSetupConfig) error {
	if len(cfg.SecretsMount) == 0 {
		return nil
	}

	// #nosec G301 -- dir must be traversable by the non-root remote user; per-file 0600 enforces secrecy.
	if err := os.MkdirAll(config.SecretsMountDir, 0o755); err != nil {
		return fmt.Errorf("create secrets mount dir: %w", err)
	}

	user := config.GetRemoteUser(cfg.SetupInfo)
	for _, entry := range cfg.SecretsMount {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		if err := writeSecretFile(name, value, user); err != nil {
			return err
		}
	}

	return nil
}

func writeSecretFile(name, value, user string) error {
	path, err := secretMountPath(name)
	if err != nil {
		return err
	}

	// Remove any pre-planted symlink (unlinks the link, not its target), then
	// O_EXCL-create so the write cannot be redirected outside SecretsMountDir.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("prepare secret file %s: %w", name, err)
	}
	f, err := os.OpenFile(
		path,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	) // #nosec G304 -- path validated to a plain filename under SecretsMountDir.
	if err != nil {
		return fmt.Errorf("write secret file %s: %w", name, err)
	}
	if _, err := f.WriteString(value); err != nil {
		_ = f.Close()
		return fmt.Errorf("write secret file %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write secret file %s: %w", name, err)
	}
	if err := copy2.Chown(path, user); err != nil {
		return fmt.Errorf("chown secret file %s: %w", name, err)
	}

	return nil
}

// secretMountPath resolves target to a flat file under SecretsMountDir. Only a
// single path element is allowed, blocking both traversal and symlinked dirs.
func secretMountPath(target string) (string, error) {
	if target != filepath.Base(target) || target == "." || target == ".." ||
		strings.ContainsRune(target, '/') || strings.ContainsRune(target, filepath.Separator) {
		return "", fmt.Errorf(
			"invalid secret mount target %q: must be a plain filename under %s",
			target,
			config.SecretsMountDir,
		)
	}

	return filepath.Join(config.SecretsMountDir, target), nil
}

func writeResultFile(cfg *ContainerSetupConfig) {
	rawBytes, err := json.Marshal(cfg.SetupInfo)
	if err != nil {
		log.Warnf("error marshal result: %v", err)
		return
	}

	if err := writeResultFileTo(pkgconfig.DevContainerResultPath, rawBytes); err != nil {
		log.Debugf(
			"%s is not writable (%v), falling back to %s",
			pkgconfig.DevContainerResultPath,
			err,
			pkgconfig.DevContainerResultFallbackPath,
		)
		if err := writeResultFileTo(
			pkgconfig.DevContainerResultFallbackPath,
			rawBytes,
		); err != nil {
			log.Warnf("error write result to %s: %v", pkgconfig.DevContainerResultFallbackPath, err)
		}
	}
}

// writeResultFileTo writes rawBytes to path at 0644: readable by any
// container user, not just root, since getContainerResult and
// portOptionsFromResult read it over sessions authenticated as either.
// Goes through sharedfile rather than raw os.ReadFile/os.WriteFile so a
// symlink or FIFO planted at this fixed, predictable path is rejected
// rather than followed or hung on, matching the coordination files this
// package's other callers protect the same way.
func writeResultFileTo(path string, rawBytes []byte) error {
	existing, _ := sharedfile.ReadFile(path)
	if string(rawBytes) == string(existing) {
		// Widen even when skipping the write: a stale file left at a
		// restrictive mode by a pre-fix binary must still get readable by
		// the other session's user, not just on the next content change.
		return sharedfile.WidenWithSudoFallback(context.Background(), path, 0o644)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return sharedfile.WriteFile(path, rawBytes, 0o644)
}

func setupWorkspaceOwnership(cfg *ContainerSetupConfig) error {
	if err := chownWorkspace(cfg.SetupInfo, cfg.ChownProjects); err != nil {
		return fmt.Errorf("failed to chown workspace: %w", err)
	}

	if err := linkRootHome(cfg.SetupInfo); err != nil {
		log.Errorf("Error linking /home/root: %v", err)
	}

	if err := chownAgentSock(cfg.SetupInfo); err != nil {
		return fmt.Errorf("chown ssh agent sock file: %w", err)
	}

	return nil
}

func setupEnvironment(cfg *ContainerSetupConfig) error {
	log.Debugf("patching etc environment")

	if err := patchEtcEnvironment(cfg.SetupInfo.MergedConfig); err != nil {
		return fmt.Errorf("patch etc environment: %w", err)
	}

	if err := patchEtcEnvironmentFlags(cfg.ExtraWorkspaceEnv); err != nil {
		return fmt.Errorf("patch etc environment from flags: %w", err)
	}

	if err := patchEtcProfile(); err != nil {
		return fmt.Errorf("patch etc profile: %w", err)
	}

	return nil
}

func setupOptionalFeatures(ctx context.Context, cfg *ContainerSetupConfig) {
	if err := setupKubeConfig(ctx, cfg.SetupInfo, cfg.TunnelClient); err != nil {
		log.Errorf("setup KubeConfig: %v", err)
	}

	if cfg.PlatformOptions != nil {
		if err := setupPlatformGitCredentials(
			ctx,
			config.GetRemoteUser(cfg.SetupInfo),
			cfg.PlatformOptions,
		); err != nil {
			log.Errorf("setup platform git credentials: %v", err)
		}
	}
}

func linkRootHome(setupInfo *config.Result) error {
	user := config.GetRemoteUser(setupInfo)
	if user != "root" {
		return nil
	}

	home, err := command.GetHome(user)
	if err != nil {
		return fmt.Errorf("find root home: %w", err)
	} else if home == "/home/root" {
		return nil
	}

	_, err = os.Stat("/home/root")
	if err == nil {
		return nil
	}

	// link /home/root to the root home
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll("/home", 0o755)
	if err != nil {
		return fmt.Errorf("create /home folder: %w", err)
	}

	err = os.Symlink(home, "/home/root")
	if err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	return nil
}

func chownWorkspace(setupInfo *config.Result, recursive bool) error {
	user := config.GetRemoteUser(setupInfo)
	// Marker content is the workspace ID, not empty: a snapshot-restored
	// container runs the ORIGINAL workspace's committed image, which already
	// carries a chownWorkspace marker from when that original workspace was
	// set up. That marker says nothing about whether THIS container's freshly
	// restored (root-owned, just-extracted) volume content has been chowned,
	// so an empty/content-agnostic marker would wrongly skip chown here and
	// leave the workspace folder inaccessible to the remote user.
	exists, err := markerFileExists("chownWorkspace", os.Getenv(pkgconfig.EnvWorkspaceID))
	if err != nil {
		return err
	} else if exists {
		return nil
	}

	workspaceRoot := filepath.Dir(setupInfo.SubstitutionContext.ContainerWorkspaceFolder)

	if workspaceRoot != "/" {
		log.Infof("chown workspace: user=%s, workspaceRoot=%s", user, workspaceRoot)
		err = copy2.Chown(workspaceRoot, user)
		if err != nil {
			log.Warn(err)
		}
	}

	if recursive {
		log.Infof(
			"chown workspace recursively: user=%s, workspaceFolder=%s",
			user,
			setupInfo.SubstitutionContext.ContainerWorkspaceFolder,
		)
		err = copy2.ChownR(setupInfo.SubstitutionContext.ContainerWorkspaceFolder, user)
		// Best effort: some entries (e.g. read-only .git pack files on a
		// virtiofs share) legitimately cannot be chowned. The remote user can
		// still work in the tree, so this is not worth a warning.
		if err != nil {
			log.Debugf("chown workspace: some entries could not be chowned: %v", err)
		}
	}

	return nil
}

func patchEtcProfile() error {
	exists, err := markerFileExists("patchEtcProfile", "")
	if err != nil {
		return err
	} else if exists {
		return nil
	}

	out, err := exec.Command("sh", "-c", `sed -i -E 's/((^|\s)PATH=)([^\$]*)$/\1${PATH:-\3}/g' /etc/profile || true`).
		CombinedOutput()
	if err != nil {
		return fmt.Errorf("create remote environment: %v: %w", string(out), err)
	}

	return nil
}

func patchEtcEnvironmentFlags(workspaceEnv []string) error {
	if len(workspaceEnv) == 0 {
		return nil
	}

	// make sure we sort the strings
	sort.Strings(workspaceEnv)

	// check if we need to update env
	exists, err := markerFileExists("patchEtcEnvironmentFlags", strings.Join(workspaceEnv, "\n"))
	if err != nil {
		return err
	} else if exists {
		return nil
	}

	// update env
	envfile.MergeAndApply(config.ListToObject(workspaceEnv))
	return nil
}

func patchEtcEnvironment(mergedConfig *config.MergedDevContainerConfig) error {
	if len(mergedConfig.RemoteEnv) == 0 {
		return nil
	}

	setEnvs, unsetKeys := splitRemoteEnv(mergedConfig.RemoteEnv)
	marker := buildEnvMarker(setEnvs, unsetKeys)

	exists, err := markerFileExists("patchEtcEnvironment", marker)
	if err != nil {
		return err
	} else if exists {
		return nil
	}

	envfile.MergeAndApply(setEnvs)
	for _, k := range unsetKeys {
		if err := os.Unsetenv(k); err != nil {
			return fmt.Errorf("unset env %s: %w", k, err)
		}
	}

	return nil
}

func splitRemoteEnv(
	remoteEnv map[string]*string,
) (map[string]string, []string) {
	setEnvs := map[string]string{}
	unsetKeys := []string{}
	for k, v := range remoteEnv {
		if v == nil {
			unsetKeys = append(unsetKeys, k)
		} else {
			setEnvs[k] = *v
		}
	}
	return setEnvs, unsetKeys
}

func buildEnvMarker(
	setEnvs map[string]string,
	unsetKeys []string,
) string {
	lines := make([]string, 0, len(setEnvs))
	for k, v := range setEnvs {
		lines = append(lines, k+"=\""+v+"\"")
	}
	sort.Strings(lines)
	sort.Strings(unsetKeys)
	return strings.Join(lines, "\n") +
		"\n---unset---\n" +
		strings.Join(unsetKeys, "\n")
}

func chownAgentSock(setupInfo *config.Result) error {
	user := config.GetRemoteUser(setupInfo)
	agentSockFile := os.Getenv("SSH_AUTH_SOCK")
	if agentSockFile != "" {
		err := copy2.ChownR(filepath.Dir(agentSockFile), user)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

// setupKubeConfig retrieves and stores a KubeConfig file in the default location `$HOME/.kube/config`.
// It merges our KubeConfig with existing ones.
func setupKubeConfig(
	ctx context.Context,
	setupInfo *config.Result,
	tunnelClient tunnel.TunnelClient,
) error {
	if shouldSkipKubeConfig(tunnelClient) {
		return nil
	}

	kubeConfigRes, err := tunnelClient.KubeConfig(ctx, &tunnel.Message{})
	if err != nil {
		return err
	}
	if kubeConfigRes.Message == "" {
		// Empty payload means the host had nothing to forward; trace at
		// debug so a missing ~/.kube/config is diagnosable on demand.
		log.Debug("kubeconfig RPC returned empty payload; skipping kubeconfig setup")
		return nil
	}

	log.Info("setup KubeConfig")
	if err := writeKubeConfig(setupInfo, kubeConfigRes.Message); err != nil {
		return err
	}

	if _, err := markerFileExists("setupKubeConfig", ""); err != nil {
		log.Warnf("write kubeconfig marker: %v", err)
	}
	return nil
}

func shouldSkipKubeConfig(tunnelClient tunnel.TunnelClient) bool {
	if tunnelClient == nil {
		return true
	}

	markerPath := filepath.Join(containerDataDir(), "setupKubeConfig.marker")
	info, err := os.Stat(markerPath)
	if err == nil {
		if info.Mode().Perm()&0o022 != 0 {
			log.Warnf(
				"ignoring insecure marker permissions: %s (%#o)",
				markerPath,
				info.Mode().Perm(),
			)
			return false
		}
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		log.Warnf("error checking marker file in shouldSkipKubeConfig: %v", err)
	}
	return false
}

func writeKubeConfig(setupInfo *config.Result, configData string) error {
	user := config.GetRemoteUser(setupInfo)
	homeDir, err := command.GetHome(user)
	if err != nil {
		return err
	}

	kubeDir := filepath.Join(homeDir, ".kube")
	if err := os.MkdirAll(
		kubeDir,
		0o700,
	); err != nil { // #nosec G301 -- kube directory should be user-private
		return err
	}

	configPath := filepath.Join(kubeDir, "config")
	if err := mergeKubeConfig(configPath, configData); err != nil {
		return err
	}

	return copy2.ChownR(kubeDir, user)
}

func mergeKubeConfig(configPath, newConfigData string) error {
	existingConfig, err := clientcmd.LoadFromFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	existingConfig = ensureKubeConfigMaps(existingConfig)

	kubeConfig, err := clientcmd.Load([]byte(newConfigData))
	if err != nil {
		return err
	}

	maps.Copy(existingConfig.Clusters, kubeConfig.Clusters)
	maps.Copy(existingConfig.AuthInfos, kubeConfig.AuthInfos)
	maps.Copy(existingConfig.Contexts, kubeConfig.Contexts)
	if kubeConfig.CurrentContext != "" {
		existingConfig.CurrentContext = kubeConfig.CurrentContext
	}

	return clientcmd.WriteToFile(*existingConfig, configPath)
}

func ensureKubeConfigMaps(config *clientcmdapi.Config) *clientcmdapi.Config {
	if config == nil {
		config = clientcmdapi.NewConfig()
	}
	if config.Clusters == nil {
		config.Clusters = map[string]*clientcmdapi.Cluster{}
	}
	if config.AuthInfos == nil {
		config.AuthInfos = map[string]*clientcmdapi.AuthInfo{}
	}
	if config.Contexts == nil {
		config.Contexts = map[string]*clientcmdapi.Context{}
	}
	return config
}

func markerFileExists(markerName string, markerContent string) (bool, error) {
	markerName = filepath.Join(containerDataDir(), markerName+".marker")
	t, err := os.ReadFile(markerName)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	} else if err == nil && (markerContent == "" || string(t) == markerContent) {
		return true, nil
	}

	// write marker
	dir := filepath.Dir(markerName)
	_ = os.MkdirAll(
		dir,
		0o755,
	) // #nosec G301 -- Standard directory permissions
	err = os.WriteFile(markerName, []byte(markerContent), 0o600)
	if err != nil {
		return false, fmt.Errorf("write marker: %w", err)
	}

	return false, nil
}

// writableContainerDataDirOnce caches the resolved container data dir for
// the process lifetime: containerDataDir may be called many times (once per
// marker check) and re-probing write access each time would be wasteful.
var writableContainerDataDirOnce = sync.OnceValue(func() string {
	if err := os.MkdirAll(pkgconfig.ContainerDataDir, 0o755); err == nil && // #nosec G301
		dirIsWritable(pkgconfig.ContainerDataDir) {
		return pkgconfig.ContainerDataDir
	}
	// Non-root containers (e.g. OpenShift's restricted SCC) can't write to
	// /var/devsy, whether because it can't be created or because it already
	// exists root-owned; fall back to the agreed-on path every reader checks.
	fallback := pkgconfig.ContainerDataDirFallback
	log.Debugf(
		"%s is not writable, using %s for container-local scratch data",
		pkgconfig.ContainerDataDir,
		fallback,
	)
	return fallback
})

// dirIsWritable reports whether dir accepts new files for the current user.
// os.MkdirAll alone can't tell: it succeeds when the directory already
// exists even if it's root-owned and unwritable by a non-root container's
// user, so callers that need real write access must probe it directly.
func dirIsWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".devsy-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// containerDataDir returns config.ContainerDataDir when writable (the
// common, root-owned case), falling back to config.ContainerDataDirFallback
// for non-root containers that can't create it.
func containerDataDir() string {
	return writableContainerDataDirOnce()
}

func setupPlatformGitCredentials(
	ctx context.Context,
	userName string,
	platformOptions *devsy.PlatformOptions,
) error {
	// platform is not enabled, skip
	if !platformOptions.Enabled {
		return nil
	}

	// setup platform git user
	if err := setupPlatformGitUser(ctx, userName, platformOptions); err != nil {
		return err
	}

	// setup platform git http credentials
	err := setupPlatformGitHTTPCredentials(ctx, userName, platformOptions)
	if err != nil {
		log.Errorf("Error setting up platform git http credentials: %v", err)
	}

	// setup platform git ssh keys
	err = setupPlatformGitSSHKeys(userName, platformOptions)
	if err != nil {
		log.Errorf("Error setting up platform git ssh keys: %v", err)
	}

	return nil
}

func setupPlatformGitUser(
	ctx context.Context,
	userName string,
	platformOptions *devsy.PlatformOptions,
) error {
	if platformOptions.UserCredentials.GitUser == "" ||
		platformOptions.UserCredentials.GitEmail == "" {
		return nil
	}

	gitUser, err := gitcredentials.GetUser(ctx, userName, "")
	if err == nil && gitUser.Name == "" && gitUser.Email == "" {
		log.Info("Setup workspace git user and email")
		err := gitcredentials.SetUser(ctx, userName, &gitcredentials.GitUser{
			Name:  platformOptions.UserCredentials.GitUser,
			Email: platformOptions.UserCredentials.GitEmail,
		})
		if err != nil {
			return fmt.Errorf("set git user: %w", err)
		}
	}

	return nil
}

func setupPlatformGitHTTPCredentials(
	ctx context.Context,
	userName string,
	platformOptions *devsy.PlatformOptions,
) error {
	if !platformOptions.Enabled || len(platformOptions.UserCredentials.GitHttp) == 0 {
		return nil
	}

	log.Info("Setup platform user git http credentials")
	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}
	err = gitcredentials.ConfigureHelper(ctx, binaryPath, userName, -1)
	if err != nil {
		return fmt.Errorf("configure git helper: %w", err)
	}

	return nil
}

func setupPlatformGitSSHKeys(
	userName string,
	platformOptions *devsy.PlatformOptions,
) error {
	if !platformOptions.Enabled || len(platformOptions.UserCredentials.GitSsh) == 0 {
		return nil
	}

	log.Info("Setup platform user git ssh keys")
	homeFolder, err := command.GetHome(userName)
	if err != nil {
		return err
	}

	// write ssh keys to ~/.ssh/id_rsa
	sshFolder := filepath.Join(homeFolder, ".ssh")
	err = os.MkdirAll(sshFolder, 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	_ = copy2.Chown(sshFolder, userName)

	// delete previous keys
	if err := removeStalePlatformSSHKeys(
		sshFolder,
		len(platformOptions.UserCredentials.GitSsh),
	); err != nil {
		return err
	}

	// write new keys
	writePlatformSSHKeys(sshFolder, userName, platformOptions.UserCredentials.GitSsh)

	return nil
}

func removeStalePlatformSSHKeys(sshFolder string, keyCount int) error {
	files, err := os.ReadDir(sshFolder)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "platform_git_ssh_") {
			continue
		}

		fileName := strings.TrimPrefix(file.Name(), "platform_git_ssh_")
		index, err := strconv.Atoi(fileName)
		if err != nil {
			continue
		}
		if index < keyCount {
			continue
		}

		err = os.Remove(filepath.Join(sshFolder, file.Name()))
		if err != nil {
			log.Warnf("Error removing previous platform git ssh key: %v", err)
		}
	}
	return nil
}

func writePlatformSSHKeys(
	sshFolder, userName string,
	keys []devsy.PlatformGitSshCredentials,
) {
	for i, key := range keys {
		fileName := filepath.Join(sshFolder, fmt.Sprintf("platform_git_ssh_%d", i))
		if err := writePlatformSSHKey(fileName, key.Key, userName); err != nil {
			log.Warnf("Error writing platform git ssh key: %v", err)
			// remove any stale key file so we don't leave old credentials behind
			if removeErr := os.Remove(
				fileName,
			); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				log.Warnf("Error removing stale platform git ssh key: %v", removeErr)
			}
		}
	}
}

func writePlatformSSHKey(fileName, encodedKey, userName string) error {
	decoded, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(fileName, decoded, 0o600); err != nil {
		return err
	}

	// do not exit on error, we can have non-fatal errors
	if err := copy2.Chown(fileName, userName); err != nil {
		log.Warnf("Error chowning platform git ssh keys: %v", err)
	}
	return nil
}

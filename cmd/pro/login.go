package pro

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/blang/semver/v4"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	providercmd "github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	versionpkg "github.com/devsy-org/devsy/pkg/version"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

const (
	PROVIDER_BINARY = "PRO_PROVIDER"
)

// LoginCmd holds the login cmd flags.
type LoginCmd struct {
	proflags.GlobalFlags

	AccessKey      string
	Provider       string
	Version        string
	ProviderSource string

	Options []string

	Login        bool
	Use          bool
	ForceBrowser bool
}

// NewLoginCmd creates a new command.
func NewLoginCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &LoginCmd{
		GlobalFlags: *flags,
	}
	loginCmd := &cobra.Command{
		Use:   "login HOST",
		Short: "Log into a Devsy Pro instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	loginCmd.Flags().
		StringVar(&cmd.AccessKey, "access-key", "", "If defined will use the given access key to login")
	loginCmd.Flags().
		BoolVar(&cmd.Login, "login", true, "If enabled will automatically try to log into Devsy Pro")
	loginCmd.Flags().
		BoolVar(&cmd.Use, "use", true, "If enabled will automatically activate the provider")
	loginCmd.Flags().
		StringVar(&cmd.Provider, "provider", "", "Optional name how the Devsy Pro provider will be named")
	loginCmd.Flags().
		StringVar(&cmd.Version, "version", "", "The version to use for the Devsy provider")
	loginCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	loginCmd.Flags().
		BoolVar(&cmd.ForceBrowser, "force-browser", false, "Force login through browser")

	loginCmd.Flags().
		StringVar(&cmd.ProviderSource, "provider-source", "", "The source of the provider")
	_ = loginCmd.Flags().MarkHidden("provider-source")
	return loginCmd
}

func (cmd *LoginCmd) Run(ctx context.Context, fullURL string) error {
	fullURL, err := cmd.normalizeURL(fullURL)
	if err != nil {
		return err
	}

	devsyConfig, currentInstance, err := cmd.resolveInstance(fullURL)
	if err != nil {
		return err
	}

	devsyConfig, err = cmd.ensureProvider(devsyConfig, currentInstance, fullURL)
	if err != nil {
		return err
	}

	return cmd.loginAndConfigure(ctx, devsyConfig, fullURL)
}

func (cmd *LoginCmd) normalizeURL(fullURL string) (string, error) {
	if strings.HasPrefix(fullURL, "http://") {
		return "", fmt.Errorf("http is not supported for Devsy Pro, use https:// instead")
	}
	if !strings.HasPrefix(fullURL, "https://") {
		return "https://" + fullURL, nil
	}
	if cmd.Provider != "" && len(cmd.Provider) > 32 {
		return "", fmt.Errorf("cannot use a provider name greater than 32 characters")
	}
	return fullURL, nil
}

func (cmd *LoginCmd) resolveInstance(
	fullURL string,
) (*config.Config, *provider.ProInstance, error) {
	parsedURL, err := url.Parse(fullURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid url %s: %w", fullURL, err)
	}
	host := parsedURL.Host

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return nil, nil, err
	}

	proInstances, err := workspace.ListProInstances(devsyConfig)
	if err != nil {
		return nil, nil, err
	}

	currentInstance := findInstance(proInstances, host)
	if currentInstance != nil {
		cmd.Provider = currentInstance.Provider
		return devsyConfig, currentInstance, nil
	}

	if err := cmd.resolveNewProviderName(devsyConfig, host); err != nil {
		return nil, nil, err
	}

	return devsyConfig, nil, nil
}

func findInstance(instances []*provider.ProInstance, host string) *provider.ProInstance {
	for _, inst := range instances {
		if inst.Host == host {
			return inst
		}
	}
	return nil
}

func (cmd *LoginCmd) resolveNewProviderName(devsyConfig *config.Config, host string) error {
	if cmd.Provider == "" {
		cmd.Provider = config.ProReleaseName
	}
	cmd.Provider = provider.ToProInstanceID(cmd.Provider)

	providers, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return fmt.Errorf("load providers: %w", err)
	}

	if providers[cmd.Provider] != nil {
		cmd.Provider = provider.ToProInstanceID(config.BinaryName + "-" + host)
		if providers[cmd.Provider] != nil {
			return fmt.Errorf(
				"provider %s already exists, choose a different name via --provider",
				cmd.Provider,
			)
		}
	}

	return nil
}

func (cmd *LoginCmd) ensureProvider(
	devsyConfig *config.Config,
	currentInstance *provider.ProInstance,
	fullURL string,
) (*config.Config, error) {
	if currentInstance != nil {
		return devsyConfig, nil
	}

	parsedURL, _ := url.Parse(fullURL)
	instance := &provider.ProInstance{
		Provider:          cmd.Provider,
		Host:              parsedURL.Host,
		CreationTimestamp: types.Now(),
	}

	if err := cmd.addProviderByVersion(devsyConfig, fullURL); err != nil {
		return nil, err
	}

	if err := provider.SaveProInstanceConfig(devsyConfig.DefaultContext, instance); err != nil {
		return nil, err
	}

	return config.LoadConfig(devsyConfig.DefaultContext, cmd.Provider)
}

func (cmd *LoginCmd) addProviderByVersion(devsyConfig *config.Config, fullURL string) error {
	remoteVersion, err := platform.GetDevsyVersion(fullURL)
	if err != nil {
		return err
	}

	rv, err := semver.Parse(strings.TrimPrefix(remoteVersion, "v"))
	if err != nil {
		return fmt.Errorf("invalid version %s: %w", remoteVersion, err)
	}

	if rv.LT(semver.Version{Major: 0, Minor: 6, Patch: 999}) &&
		remoteVersion != versionpkg.DevVersion {
		log.Debug("remote version < 0.7.0, installing proxy provider")
		return cmd.addLoftProvider(devsyConfig, fullURL)
	}

	_, err = workspace.AddProvider(devsyConfig, cmd.Provider, "pro")
	return err
}

func (cmd *LoginCmd) loginAndConfigure(
	ctx context.Context,
	devsyConfig *config.Config,
	fullURL string,
) error {
	providerConfig, err := provider.LoadProviderConfig(devsyConfig.DefaultContext, cmd.Provider)
	if err != nil {
		return err
	}

	if cmd.Login {
		err = login(
			ctx,
			devsyConfig,
			fullURL,
			cmd.Provider,
			cmd.AccessKey,
			false,
			cmd.ForceBrowser,
		)
		if err != nil {
			return err
		}
		log.Infof("logged into Devsy Pro instance: url=%s", fullURL)
	}

	if cmd.Use {
		err := providercmd.ConfigureProvider(ctx, providercmd.ProviderOptionsConfig{
			Provider:       providerConfig,
			Context:        devsyConfig.DefaultContext,
			UserOptions:    cmd.Options,
			Reconfigure:    false,
			SkipRequired:   false,
			SkipInit:       false,
			SkipSubOptions: false,
			SingleMachine:  nil,
		})
		if err != nil {
			return fmt.Errorf("configure provider: %w", err)
		}
	}

	log.Info("configured Devsy Pro")
	return nil
}

func (cmd *LoginCmd) addLoftProvider(
	devsyConfig *config.Config,
	url string,
) error {
	// find out loft version
	err := cmd.resolveProviderSource(url)
	if err != nil {
		return err
	}

	// add the provider
	log.Infof("Add Devsy Pro provider")

	// is development?
	if cmd.ProviderSource == config.RepoSlug+"@v0.0.0" {
		log.Debugf("Add development provider")
		_, err = workspace.AddProviderRaw(workspace.ProviderParams{
			DevsyConfig:  devsyConfig,
			ProviderName: cmd.Provider,
			Source:       &provider.ProviderSource{},
			Raw:          []byte(fallbackProvider),
		})
		if err != nil {
			return err
		}
	} else {
		_, err = workspace.AddProvider(devsyConfig, cmd.Provider, cmd.ProviderSource)
		if err != nil {
			return err
		}
	}

	return nil
}

func (cmd *LoginCmd) resolveProviderSource(url string) error {
	if cmd.ProviderSource != "" {
		return nil
	}
	if cmd.Version != "" {
		cmd.ProviderSource = config.RepoSlug + "@" + cmd.Version
		return nil
	}

	version, err := platform.GetDevsyVersion(url)
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}
	cmd.ProviderSource = config.RepoSlug + "@" + version

	return nil
}

func login(
	ctx context.Context,
	devsyConfig *config.Config,
	url string,
	providerName string,
	accessKey string,
	skipBrowserLogin, forceBrowser bool,
) error {
	configPath, err := platform.DevsyConfigPath(devsyConfig.DefaultContext, providerName)
	if err != nil {
		return err
	}
	loader, err := client.NewClientFromPath(configPath)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}

	if accessKey == "" {
		accessKey = loader.Config().AccessKey
	}

	// log in
	url = strings.TrimSuffix(url, "/")
	if err := ctx.Err(); err != nil {
		return err
	}
	if accessKey != "" && !forceBrowser {
		err = loader.LoginWithAccessKey(url, accessKey, true, true)
	} else {
		if skipBrowserLogin {
			return fmt.Errorf("unable to login to loft host")
		}
		err = loader.Login(url, true)
	}
	if err != nil {
		return err
	}

	return nil
}

var fallbackProvider = `name: devsy-pro
version: v0.0.0
icon: https://devsy.sh/assets/devsy.svg
description: Devsy Pro
options:
  DEVSY_CONFIG:
    global: true
    hidden: true
    required: true
    default: "${PROVIDER_FOLDER}/devsy-config.json"
binaries:
  PRO_PROVIDER:
    - os: linux
      arch: amd64
      path: /usr/local/bin/devsy
    - os: linux
      arch: arm64
      path: /usr/local/bin/devsy
    - os: darwin
      arch: amd64
      path: /usr/local/bin/devsy
    - os: darwin
      arch: arm64
      path: /usr/local/bin/devsy
    - os: windows
      arch: amd64
      path: "C:\\Users\\pasca\\workspace\\devsy\\desktop\\src-tauri\\bin\\devsy-x86_64-pc-windows-msvc.exe"
exec:
  proxy:
    up: |-
      ${PRO_PROVIDER} pro provider up
    ssh: |-
      ${PRO_PROVIDER} pro provider ssh
    stop: |-
      ${PRO_PROVIDER} pro provider stop
    status: |-
      ${PRO_PROVIDER} pro provider status
    delete: |-
      ${PRO_PROVIDER} pro provider delete
    health: |-
      ${PRO_PROVIDER} pro provider health
    daemon:
      start: |-
        ${PRO_PROVIDER} pro provider daemon start
      status: |-
        ${PRO_PROVIDER} pro provider daemon status
    create:
      workspace: |-
        ${PRO_PROVIDER} pro provider create workspace
    get:
      workspace: |-
        ${PRO_PROVIDER} pro provider get workspace
      self: |-
        ${PRO_PROVIDER} pro provider get self
      version: |-
        ${PRO_PROVIDER} pro provider get version
    update:
      workspace: |-
        ${PRO_PROVIDER} pro provider update workspace
    watch:
      workspaces: |-
        ${PRO_PROVIDER} pro provider watch workspaces
    list:
      workspaces: |-
        ${PRO_PROVIDER} pro provider list workspaces
      projects: |-
        ${PRO_PROVIDER} pro provider list projects
      templates: |-
        ${PRO_PROVIDER} pro provider list templates
      clusters: |-
        ${PRO_PROVIDER} pro provider list clusters
`

//go:build !windows

package agentcontainer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// encodeSecretsEnv base64-encodes each entry so values may contain newlines or
// any bytes (e.g. PEM keys) without corrupting the DEVSY_SECRETS_ENV carrier.
func encodeSecretsEnv(secretsEnv []string) string {
	encoded := make([]string, len(secretsEnv))
	for i, entry := range secretsEnv {
		encoded[i] = base64.StdEncoding.EncodeToString([]byte(entry))
	}
	return strings.Join(encoded, "\n")
}

// secretsEnvOverride returns os.Environ() with the encoded carrier appended, or
// nil when there are no secrets. Shared so the re-exec paths cannot diverge on encoding.
func secretsEnvOverride(secretsEnv []string) []string {
	if len(secretsEnv) == 0 {
		return nil
	}
	return append(os.Environ(), config2.EnvSecretsEnv+"="+encodeSecretsEnv(secretsEnv))
}

func secretsEnvFromEnvironment() []string {
	raw := os.Getenv(config2.EnvSecretsEnv)
	if raw == "" {
		return nil
	}
	// Clear the carrier so lifecycle hooks (which inherit this process's env)
	// cannot read the encoded secrets back out of DEVSY_SECRETS_ENV.
	_ = os.Unsetenv(config2.EnvSecretsEnv)

	parts := strings.Split(raw, "\n")
	entries := make([]string, 0, len(parts))
	for _, p := range parts {
		decoded, err := base64.StdEncoding.DecodeString(p)
		if err != nil {
			continue
		}
		entries = append(entries, string(decoded))
	}

	return entries
}

// DeferredHooksCmd runs deferred lifecycle hooks as a detached background process.
type DeferredHooksCmd struct {
	*flags.GlobalFlags
	SetupInfo      string
	Prebuild       bool
	DotfilesRepo   string
	DotfilesScript string
}

// NewDeferredHooksCmd creates a new command.
func NewDeferredHooksCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeferredHooksCmd{
		GlobalFlags: flags,
	}
	deferredCmd := &cobra.Command{
		Use:   "deferred-hooks",
		Short: "Runs deferred lifecycle hooks (phases after waitFor)",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		deferredCmd,
		cliflags.String(&cmd.SetupInfo, names.SetupInfo, "", "The container setup info"),
		cliflags.Bool(&cmd.Prebuild, names.Prebuild, false, "If true, prebuild lifecycle mode"),
		cliflags.String(&cmd.DotfilesRepo, names.DotfilesRepo, "", "Dotfiles repository URL"),
		cliflags.String(
			&cmd.DotfilesScript,
			names.DotfilesScript,
			"",
			"Dotfiles install script path",
		),
	)
	_ = deferredCmd.MarkFlagRequired(names.SetupInfo)
	return deferredCmd
}

// Run executes the deferred lifecycle hooks.
func (cmd *DeferredHooksCmd) Run(ctx context.Context) error {
	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	if err := json.Unmarshal([]byte(decompressed), setupInfo); err != nil {
		return err
	}

	log.Debugf("running deferred lifecycle hooks")
	deferred, err := setup.RunPreAttachHooks(ctx, setupInfo, setup.PreAttachOptions{
		Prebuild: cmd.Prebuild,
		Dotfiles: setup.DotfilesConfig{
			Repository:    cmd.DotfilesRepo,
			InstallScript: cmd.DotfilesScript,
			RemoteUser:    config.GetRemoteUser(setupInfo),
		},
		SecretsEnv:   secretsEnvFromEnvironment(),
		SecretsMount: setup.MountSecretsForRedaction(),
	})
	if err != nil {
		return fmt.Errorf("deferred hooks setup: %w", err)
	}

	if err := deferred.Run(); err != nil {
		return fmt.Errorf("deferred lifecycle hooks: %w", err)
	}

	return nil
}

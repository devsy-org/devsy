package agentworkspace

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/credentials"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// SetupGPGCmd holds the setupGPG cmd flags.
type SetupGPGCmd struct {
	*flags.GlobalFlags

	OwnerTrust string
	SocketPath string
	GitKey     string
}

// NewSetupGPGCmd creates a new command.
func NewSetupGPGCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupGPGCmd{
		GlobalFlags: flags,
	}
	setupGPGCmd := &cobra.Command{
		Use:   "setup-gpg",
		Short: "setups gpg-agent forwarding in the container",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		setupGPGCmd,
		cliflags.String(
			&cmd.OwnerTrust,
			names.OwnerTrust,
			"",
			"GPG Owner trust to import in armor form",
		),
		cliflags.String(&cmd.SocketPath, names.SocketPath, "", "path to the gpg socket forwarded"),
		cliflags.String(&cmd.GitKey, names.GitKey, "", "gpg key to use for git commit signing"),
	)
	return setupGPGCmd
}

// will forward a local gpg-agent into the remote container
// this works by
//
// - stopping remote gpg-agent and removing the sockets
// - exporting local public keys and owner trust
// - importing those into the container
// - ensuring the gpg-agent is stopped in the container
// - starting a reverse-tunnel of the local unix socket to remote
// - ensuring paths and permissions are correctly set in the remote.
func (cmd *SetupGPGCmd) Run(ctx context.Context) error {
	log.Debugf("Initializing gpg-agent forwarding")

	publicKey, ownerTrust, err := fetchAndDecodeKeys(cmd.OwnerTrust)
	if err != nil {
		return err
	}

	gpgConf := gpg.GPGConf{
		PublicKey:  publicKey,
		OwnerTrust: ownerTrust,
		SocketPath: cmd.SocketPath,
		GitKey:     cmd.GitKey,
	}

	if err := configureGPGAgent(&gpgConf); err != nil {
		return err
	}

	if gpgConf.GitKey != "" {
		log.Debugf("Setup git signing key")
		if err := gitcredentials.SetupGpgGitKey(ctx, gpgConf.GitKey); err != nil {
			log.Warnf("Setup git signing key failed (non-fatal): %v", err)
		}
	}

	return nil
}

func fetchAndDecodeKeys(ownerTrustB64 string) ([]byte, []byte, error) {
	log.Debugf("Fetching public key")
	rawPublicKeys, err := getPublicKeys()
	if err != nil {
		return nil, nil, fmt.Errorf("fetch public key: %w", err)
	}

	log.Debugf("Decoding public key")
	publicKey, err := base64.StdEncoding.DecodeString(rawPublicKeys)
	if err != nil {
		return nil, nil, err
	}

	log.Debugf("Decoding input owner trust")
	ownerTrust, err := base64.StdEncoding.DecodeString(ownerTrustB64)
	if err != nil {
		return nil, nil, err
	}

	return publicKey, ownerTrust, nil
}

func configureGPGAgent(gpgConf *gpg.GPGConf) error {
	log.Debugf("Stopping container gpg-agent")
	if err := gpgConf.StopGpgAgent(); err != nil {
		return fmt.Errorf("stop container gpg-agent: %w", err)
	}

	log.Debugf("Importing gpg public key in container")
	if err := gpgConf.ImportGpgKey(); err != nil {
		return fmt.Errorf("import gpg public key in container: %w", err)
	}

	log.Debugf("Importing gpg owner trust in container")
	if err := gpgConf.ImportOwnerTrust(); err != nil {
		return fmt.Errorf("import gpg owner trust in container: %w", err)
	}

	log.Debugf("Ensuring paths existence and permissions")
	if err := gpgConf.SetupRemoteSocketDirTree(); err != nil {
		return fmt.Errorf("ensure paths existence and permissions: %w", err)
	}

	// Now we again kill the agent and remove the socket to really be sure every
	// thing is clean
	log.Debugf("Ensure stopping container gpg-agent")
	if err := gpgConf.StopGpgAgent(); err != nil {
		return fmt.Errorf("ensure stopping container gpg-agent: %w", err)
	}

	log.Debugf("Setup local gnupg socket links")
	if err := gpgConf.SetupRemoteSocketLink(); err != nil {
		return fmt.Errorf("setup local gnupg socket links: %w", err)
	}

	log.Debugf("Setup gpg.conf")
	if err := gpgConf.SetupGpgConf(); err != nil {
		return fmt.Errorf("setup gpg.conf: %w", err)
	}

	return nil
}

func getPublicKeys() (string, error) {
	port, err := credentials.GetPort()
	if err != nil {
		return "", fmt.Errorf("get port: %w", err)
	}

	out, err := credentials.PostWithRetry(port, "gpg-public-keys", nil)
	if err != nil {
		return "", fmt.Errorf("get public gpg keys: %w", err)
	}

	return string(out), nil
}

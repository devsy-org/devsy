package gpg

import (
	"context"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/log"
)

// DetectAgentSocketPath returns the host gpg-agent extra socket path, used to
// reverse-forward the agent into a workspace.
func DetectAgentSocketPath() (string, error) {
	out, err := exec.Command("gpgconf", "--list-dir", "agent-extra-socket").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitFormatSSH is the git gpg.format value indicating SSH-based commit signing.
const gitFormatSSH = "ssh"

// SigningKey returns the user's GPG signing key from git config, or "" when no
// key is configured or the signing format is SSH (SSH signing keys are handled
// by the separate SSH signature helper).
func SigningKey(ctx context.Context) string {
	config := git.At("").Config()
	formatStr, _ := config.Get(ctx, "gpg.format", git.ScopeDefault)
	if formatStr == gitFormatSSH {
		log.Debugf("gpg.format is %s, skipping GPG signing key", gitFormatSSH)
		return ""
	}

	result, err := config.Get(ctx, "user.signingKey", git.ScopeDefault)
	if err != nil {
		log.Debugf("no git signkey detected, skipping")
		return ""
	}

	// GPG key IDs are hex fingerprints, not file paths. If the signing key
	// looks like a file path and the format is not x509 (which legitimately
	// uses certificate file paths via gpgsm), it's an SSH key.
	if (strings.HasPrefix(result, "/") || strings.HasPrefix(result, "~")) && formatStr != "x509" {
		log.Debugf("signing key %s looks like a file path, skipping", result)
		return ""
	}

	log.Debugf("detected git sign key %s", result)
	return result
}

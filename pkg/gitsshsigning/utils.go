package gitsshsigning

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/git"
)

const (
	GPGFormatConfigKey       = "gpg.format"
	UsersSigningKeyConfigKey = "user.signingkey"
	GPGFormatSSH             = "ssh"
)

// IsLiteralSSHKey reports whether signingKey is an inline SSH public key rather than a file path.
func IsLiteralSSHKey(signingKey string) bool {
	return strings.HasPrefix(signingKey, "key::") || strings.HasPrefix(signingKey, "ssh-")
}

// ExpandSigningKeyPath expands a leading ~ or ~user prefix in an SSH signing key path.
func ExpandSigningKeyPath(signingKey string) string {
	if signingKey == "" || IsLiteralSSHKey(signingKey) {
		return signingKey
	}
	rest, ok := strings.CutPrefix(signingKey, "~")
	if !ok {
		return signingKey
	}
	if rest == "" {
		return expandCurrentUserHome("", signingKey)
	}
	if rest[0] == '/' || rest[0] == '\\' {
		return expandCurrentUserHome(rest[1:], signingKey)
	}
	return expandNamedUserHome(rest, signingKey)
}

// ResolveSigningKey expands ~ prefixes and resolves relative SSH key paths against workingDir.
func ResolveSigningKey(signingKey, workingDir string) string {
	if signingKey == "" || IsLiteralSSHKey(signingKey) {
		return signingKey
	}
	expanded := ExpandSigningKeyPath(signingKey)
	if !strings.HasPrefix(expanded, "~") && !filepath.IsAbs(expanded) && workingDir != "" {
		return filepath.Join(workingDir, expanded)
	}
	return expanded
}

// ExtractGitConfiguration reads gpg.format and user.signingkey from git config at workingDir.
func ExtractGitConfiguration(ctx context.Context, workingDir string) (string, string, error) {
	config := git.At(workingDir).Config()

	format, err := config.Get(ctx, GPGFormatConfigKey, git.ScopeDefault)
	if err != nil {
		return "", "", err
	}

	signingKey, err := config.Get(ctx, UsersSigningKeyConfigKey, git.ScopeDefault)
	if err != nil {
		return "", "", err
	}

	if format == GPGFormatSSH && signingKey != "" {
		signingKey = ResolveSigningKey(signingKey, workingDir)
	}

	return format, signingKey, nil
}

func expandCurrentUserHome(subpath, fallback string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fallback
	}
	if subpath == "" {
		return home
	}
	return filepath.Join(home, subpath)
}

func expandNamedUserHome(rest, fallback string) string {
	username := rest
	subpath := ""
	if idx := strings.IndexAny(rest, `/\`); idx >= 0 {
		username = rest[:idx]
		subpath = rest[idx+1:]
	}
	usr, err := user.Lookup(username)
	if err != nil || usr.HomeDir == "" {
		return fallback
	}
	if subpath == "" {
		return usr.HomeDir
	}
	return filepath.Join(usr.HomeDir, subpath)
}

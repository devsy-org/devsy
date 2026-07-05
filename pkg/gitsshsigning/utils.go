package gitsshsigning

import (
	"context"

	"github.com/devsy-org/devsy/pkg/git"
)

const (
	GPGFormatConfigKey       = "gpg.format"
	UsersSigningKeyConfigKey = "user.signingkey"
	GPGFormatSSH             = "ssh"
)

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

	return format, signingKey, nil
}

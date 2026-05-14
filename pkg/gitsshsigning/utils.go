package gitsshsigning

import (
	"os/exec"
	"strings"
)

const (
	GPGFormatConfigKey       = "gpg.format"
	UsersSigningKeyConfigKey = "user.signingkey"
	GPGFormatSSH             = "ssh"
)

func ExtractGitConfiguration(workingDir string) (string, string, error) {
	format, err := readGitConfigValue(GPGFormatConfigKey, workingDir)
	if err != nil {
		return "", "", err
	}

	signingKey, err := readGitConfigValue(UsersSigningKeyConfigKey, workingDir)
	if err != nil {
		return "", "", err
	}

	return format, signingKey, nil
}

func readGitConfigValue(key string, workingDir string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

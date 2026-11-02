package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type AliasReplacement struct {
	ConfigPath    string
	IncludePath   string
	CanonicalHost string
	PreferredHost string
}

func ReplaceHostAliasesWithCommit(r AliasReplacement, commit func() error) (bool, error) {
	target, err := resolveAliasTarget(r.ConfigPath, r.IncludePath)
	if err != nil {
		return false, err
	}

	exists, err := aliasTargetExists(target)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, runAliasCommit(commit)
	}

	unlock, err := lockConfigFile(target)
	if err != nil {
		return false, err
	}
	defer unlock()

	return rewriteAliasTarget(target, r.CanonicalHost, r.PreferredHost, commit)
}

func aliasTargetExists(target string) (bool, error) {
	_, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat ssh config: %w", err)
	}

	return true, nil
}

func runAliasCommit(commit func() error) error {
	if commit == nil {
		return nil
	}

	return commit()
}

func rewriteAliasTarget(
	target, canonicalHost, preferredHost string,
	commit func() error,
) (bool, error) {
	original, err := os.ReadFile(filepath.Clean(target))
	if errors.Is(err, fs.ErrNotExist) {
		return false, runAliasCommit(commit)
	}
	if err != nil {
		return false, fmt.Errorf("read ssh config: %w", err)
	}

	rewritten, found := rewriteHostLines(string(original), canonicalHost, preferredHost)
	if !found || rewritten == string(original) {
		return false, runAliasCommit(commit)
	}

	err = writeFileAtomic(target, []byte(rewritten))
	if err != nil {
		return false, fmt.Errorf("write ssh config: %w", err)
	}

	err = runAliasCommit(commit)
	if err != nil {
		return false, rollbackAliasTarget(target, original, err)
	}

	return true, nil
}

func rollbackAliasTarget(target string, original []byte, commitErr error) error {
	err := writeFileAtomic(target, original)
	if err != nil {
		return errors.Join(commitErr, fmt.Errorf("restore ssh config: %w", err))
	}

	return commitErr
}

func resolveAliasTarget(sshConfigPath, sshConfigIncludePath string) (string, error) {
	target, err := ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return "", fmt.Errorf("invalid ssh config path: %w", err)
	}

	if sshConfigIncludePath != "" {
		target, err = ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return "", fmt.Errorf("invalid ssh config include path: %w", err)
		}
	}

	return resolveSymlinkTarget(target)
}

func rewriteHostLines(content, canonicalHost, preferredHost string) (string, bool) {
	startMarker := MarkerStartPrefix + canonicalHost
	endMarker := MarkerEndPrefix + canonicalHost
	lines := strings.Split(content, "\n")
	inSection := false
	hostSeen := false
	found := false

	for i, line := range lines {
		switch {
		case isMarkerLine(line, startMarker):
			inSection = true
			hostSeen = false
		case isMarkerLine(line, endMarker):
			inSection = false
		case inSection && !hostSeen && strings.EqualFold(sshConfigKeyword(line), "host"):
			hostSeen = true
			found = true
			lines[i] = replacementHostLine(line, canonicalHost, preferredHost)
		}
	}

	return strings.Join(lines, "\n"), found
}

func replacementHostLine(line, canonicalHost, preferredHost string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	ending := ""
	if strings.HasSuffix(line, "\r") {
		ending = "\r"
	}

	return indent + hostLine(canonicalHost, preferredHost) + ending
}

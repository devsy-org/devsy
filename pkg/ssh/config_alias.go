package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func ReplaceHostAliases(
	sshConfigPath, sshConfigIncludePath, canonicalHost, preferredHost string,
) (func() error, bool, error) {
	noop := func() error { return nil }

	target, err := resolveAliasTarget(sshConfigPath, sshConfigIncludePath)
	if err != nil {
		return noop, false, err
	}

	exists, err := aliasTargetExists(target)
	if err != nil {
		return noop, false, err
	}
	if !exists {
		return noop, false, nil
	}

	unlock, err := lockConfigFile(target)
	if err != nil {
		return noop, false, err
	}
	defer unlock()

	return rewriteAliasTarget(target, canonicalHost, preferredHost)
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

func rewriteAliasTarget(target, canonicalHost, preferredHost string) (func() error, bool, error) {
	noop := func() error { return nil }

	original, err := os.ReadFile(filepath.Clean(target))
	if errors.Is(err, fs.ErrNotExist) {
		return noop, false, nil
	}
	if err != nil {
		return noop, false, fmt.Errorf("read ssh config: %w", err)
	}

	rewritten, found := rewriteHostLines(string(original), canonicalHost, preferredHost)
	if !found {
		return noop, false, nil
	}

	if rewritten == string(original) {
		return noop, true, nil
	}

	err = writeFileAtomic(target, []byte(rewritten))
	if err != nil {
		return noop, false, fmt.Errorf("write ssh config: %w", err)
	}

	restore := func() error {
		return restoreConfigFile(target, original)
	}

	return restore, true, nil
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

func restoreConfigFile(target string, original []byte) error {
	unlock, err := lockConfigFile(target)
	if err != nil {
		return err
	}
	defer unlock()

	err = writeFileAtomic(target, original)
	if err != nil {
		return fmt.Errorf("restore ssh config: %w", err)
	}

	return nil
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

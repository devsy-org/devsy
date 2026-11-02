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
	Verify        func() error
}

func ReplaceHostAliasesWithCommit(r AliasReplacement, commit func() error) (bool, error) {
	target, err := resolveWriteTarget(r.ConfigPath, r.IncludePath)
	if err != nil {
		return false, err
	}

	unlock, err := lockConfigFile(target)
	if err != nil {
		return false, err
	}
	defer unlock()

	err = runAliasVerify(r.Verify)
	if err != nil {
		return false, err
	}

	return rewriteAliasTarget(target, r.CanonicalHost, r.PreferredHost, commit)
}

func runAliasVerify(verify func() error) error {
	if verify == nil {
		return nil
	}

	return verify()
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

func resolveWriteTarget(sshConfigPath, sshConfigIncludePath string) (string, error) {
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
	hostIdx := -1
	found := false

	for i, line := range lines {
		switch {
		case isMarkerLine(line, startMarker):
			inSection = true
			hostIdx = -1
		case isMarkerLine(line, endMarker):
			found = applyHostLine(lines, hostIdx, canonicalHost, preferredHost) || found
			inSection = false
			hostIdx = -1
		case inSection && hostIdx < 0 && strings.EqualFold(sshConfigKeyword(line), "host"):
			hostIdx = i
		}
	}

	return strings.Join(lines, "\n"), found
}

func applyHostLine(lines []string, idx int, canonicalHost, preferredHost string) bool {
	if idx < 0 {
		return false
	}

	lines[idx] = replacementHostLine(lines[idx], canonicalHost, preferredHost)

	return true
}

func replacementHostLine(line, canonicalHost, preferredHost string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	ending := ""
	if strings.HasSuffix(line, "\r") {
		ending = "\r"
	}

	return indent + hostLine(canonicalHost, preferredHost) + ending
}

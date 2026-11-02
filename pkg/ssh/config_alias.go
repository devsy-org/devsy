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

// ReplaceHostAliasesWithCommit rewrites the canonical host block without checking ownership.
func ReplaceHostAliasesWithCommit(r AliasReplacement, commit func() error) (bool, error) {
	return replaceHostAliases(r, nil, commit)
}

// ReplaceHostAliasesForOwner rewrites the canonical host block only when owner owns it.
func ReplaceHostAliasesForOwner(
	r AliasReplacement,
	owner Owner,
	commit func() error,
) (bool, error) {
	policy, err := newResolvedBlockPolicy(
		owner, workspaceIDFromHost(r.CanonicalHost), "",
	)
	if err != nil {
		return false, err
	}

	return replaceHostAliases(r, policy, commit)
}

func replaceHostAliases(
	r AliasReplacement,
	policy *blockPolicy,
	commit func() error,
) (bool, error) {
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

	return rewriteAliasTarget(target, r, policy, commit)
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
	target string,
	r AliasReplacement,
	policy *blockPolicy,
	commit func() error,
) (bool, error) {
	original, err := os.ReadFile(filepath.Clean(target))
	if errors.Is(err, fs.ErrNotExist) {
		return false, runAliasCommit(commit)
	}
	if err != nil {
		return false, fmt.Errorf("read ssh config: %w", err)
	}

	rewritten, found, err := rewriteHostLines(string(original), target, r, policy)
	if err != nil {
		return false, err
	}
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

func rewriteHostLines(
	content, path string,
	r AliasReplacement,
	policy *blockPolicy,
) (string, bool, error) {
	startMarker := MarkerStartPrefix + r.CanonicalHost
	endMarker := MarkerEndPrefix + r.CanonicalHost
	lines := strings.Split(content, "\n")
	start := -1
	found := false

	for i, line := range lines {
		switch {
		case isMarkerLine(line, startMarker):
			start = i
		case start >= 0 && isMarkerLine(line, endMarker):
			err := policy.claim(path, r.CanonicalHost, lines[start:i+1])
			if err != nil {
				return "", false, err
			}
			idx := firstHostLine(lines, start, i)
			found = applyHostLine(lines, idx, r.CanonicalHost, r.PreferredHost) || found
			start = -1
		}
	}

	return strings.Join(lines, "\n"), found, nil
}

func firstHostLine(lines []string, start, end int) int {
	for i := start + 1; i < end; i++ {
		if strings.EqualFold(sshConfigKeyword(lines[i]), "host") {
			return i
		}
	}

	return -1
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

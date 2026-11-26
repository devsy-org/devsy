package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/devsy-org/devsy/pkg/util"
)

const (
	maxIncludeDepth = 5
	maxIncludeFiles = 64
)

type HostConflict struct {
	Host string
	File string
}

type conflictScan struct {
	hosts     []string
	ownHost   string
	ownMarker string
	ownEnd    string
	policy    *blockPolicy
	homeDir   string
	visited   map[string]struct{}
	seen      map[HostConflict]struct{}
	conflicts []HostConflict
}

type HostConflictQuery struct {
	ConfigPath    string
	IncludePath   string
	Hosts         []string
	CanonicalHost string
	Owner         Owner
}

// FindHostConflicts excludes any block for ownCanonicalHost without checking ownership.
func FindHostConflicts(
	sshConfigPath, sshConfigIncludePath string,
	hosts []string,
	ownCanonicalHost string,
) ([]HostConflict, error) {
	return findHostConflicts(HostConflictQuery{
		ConfigPath:    sshConfigPath,
		IncludePath:   sshConfigIncludePath,
		Hosts:         hosts,
		CanonicalHost: ownCanonicalHost,
	}, nil)
}

// FindHostConflictsForOwner excludes only blocks for the canonical host that owner owns.
func FindHostConflictsForOwner(query HostConflictQuery) ([]HostConflict, error) {
	policy, err := newResolvedBlockPolicy(
		query.Owner, workspaceIDFromHost(query.CanonicalHost), "",
	)
	if err != nil {
		return nil, err
	}

	return findHostConflicts(query, policy)
}

func findHostConflicts(query HostConflictQuery, policy *blockPolicy) ([]HostConflict, error) {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	roots := make([]string, 0, 2)
	rootPath, err := ResolveSSHConfigPath(query.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("invalid ssh config path: %w", err)
	}
	roots = append(roots, rootPath)

	if query.IncludePath != "" {
		includePath, err := ResolveSSHConfigPath(query.IncludePath)
		if err != nil {
			return nil, fmt.Errorf("invalid ssh config include path: %w", err)
		}
		roots = append(roots, includePath)
	}

	scan := &conflictScan{
		hosts:     query.Hosts,
		ownHost:   query.CanonicalHost,
		ownMarker: MarkerStartPrefix + query.CanonicalHost,
		ownEnd:    MarkerEndPrefix + query.CanonicalHost,
		policy:    policy,
		homeDir:   homeDir,
		visited:   map[string]struct{}{},
		seen:      map[HostConflict]struct{}{},
	}
	for _, root := range roots {
		err = scan.scanFile(root, 0)
		if err != nil {
			return nil, err
		}
	}

	return scan.conflicts, nil
}

func (c *conflictScan) scanFile(path string, depth int) error {
	if _, ok := c.visited[path]; ok {
		return nil
	}

	if len(c.visited) >= maxIncludeFiles {
		return fmt.Errorf(
			"ssh config include limit of %d files exceeded at %s",
			maxIncludeFiles,
			path,
		)
	}

	lines, err := readConfigLines(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ssh config %s: %w", path, err)
	}
	c.visited[path] = struct{}{}

	return c.scanLines(path, lines, depth)
}

func (c *conflictScan) scanLines(path string, lines []string, depth int) error {
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if isMarkerLine(line, c.ownMarker) {
			end := managedBlockEnd(lines, i, c.ownEnd)
			if c.policy.owns(path, c.ownHost, lines[i:end+1]) {
				i = end
			}

			continue
		}
		if isMarkerLine(line, c.ownEnd) {
			continue
		}

		err := c.scanLine(path, line, depth)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *conflictScan) scanLine(path, line string, depth int) error {
	switch strings.ToLower(sshConfigKeyword(line)) {
	case "host":
		c.recordHosts(path, line)
	case "include":
		return c.followIncludes(path, line, depth)
	}

	return nil
}

func (c *conflictScan) recordHosts(path, line string) {
	for _, token := range configArguments(line) {
		if strings.HasPrefix(token, "!") || strings.ContainsAny(token, "*?") {
			continue
		}

		for _, host := range c.hosts {
			if host == "" || !strings.EqualFold(host, token) {
				continue
			}

			conflict := HostConflict{Host: host, File: path}
			if _, ok := c.seen[conflict]; ok {
				continue
			}
			c.seen[conflict] = struct{}{}
			c.conflicts = append(c.conflicts, conflict)
		}
	}
}

func (c *conflictScan) followIncludes(path, line string, depth int) error {
	if depth >= maxIncludeDepth {
		return fmt.Errorf(
			"ssh config include depth limit of %d exceeded at %s",
			maxIncludeDepth,
			path,
		)
	}

	for _, pattern := range configArguments(line) {
		matches, err := filepath.Glob(c.expandIncludePattern(pattern))
		if err != nil {
			return fmt.Errorf("invalid ssh config include %q in %s: %w", pattern, path, err)
		}

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}

			err = c.scanFile(match, depth+1)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *conflictScan) expandIncludePattern(pattern string) string {
	switch {
	case pattern == "~":
		return c.homeDir
	case strings.HasPrefix(pattern, "~/"):
		return filepath.Join(c.homeDir, pattern[2:])
	case filepath.IsAbs(pattern):
		return pattern
	default:
		return filepath.Join(c.homeDir, ".ssh", pattern)
	}
}

func readConfigLines(path string) ([]string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, nil
	}

	lines := []string{}
	lineScanner := scanner.NewScanner(file)
	for lineScanner.Scan() {
		lines = append(lines, lineScanner.Text())
	}

	return lines, lineScanner.Err()
}

func configArguments(line string) []string {
	trimmed := strings.TrimSpace(line)
	keyword := sshConfigKeyword(trimmed)
	rest := strings.TrimSpace(trimmed[len(keyword):])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))

	return splitQuotedArguments(rest)
}

func splitQuotedArguments(rest string) []string {
	arguments := []string{}
	var current strings.Builder
	inQuote := false
	hasToken := false

	for _, r := range rest {
		switch {
		case r == '"':
			inQuote = !inQuote
			hasToken = true
		case !inQuote && unicode.IsSpace(r):
			if hasToken {
				arguments = append(arguments, current.String())
				current.Reset()
				hasToken = false
			}
		default:
			current.WriteRune(r)
			hasToken = true
		}
	}
	if hasToken {
		arguments = append(arguments, current.String())
	}

	return arguments
}

package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	ownMarker string
	ownEnd    string
	homeDir   string
	visited   map[string]struct{}
	seen      map[HostConflict]struct{}
	conflicts []HostConflict
}

func FindHostConflicts(
	sshConfigPath, sshConfigIncludePath string,
	hosts []string,
	ownCanonicalHost string,
) ([]HostConflict, error) {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	roots := make([]string, 0, 2)
	rootPath, err := ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return nil, fmt.Errorf("invalid ssh config path: %w", err)
	}
	roots = append(roots, rootPath)

	if sshConfigIncludePath != "" {
		includePath, err := ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return nil, fmt.Errorf("invalid ssh config include path: %w", err)
		}
		roots = append(roots, includePath)
	}

	scan := &conflictScan{
		hosts:     hosts,
		ownMarker: MarkerStartPrefix + ownCanonicalHost,
		ownEnd:    MarkerEndPrefix + ownCanonicalHost,
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
	inOwn := false
	for _, line := range lines {
		var skip bool
		inOwn, skip = c.trackOwnBlock(line, inOwn)
		if skip {
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

func (c *conflictScan) trackOwnBlock(line string, inOwn bool) (bool, bool) {
	switch {
	case isMarkerLine(line, c.ownMarker):
		return true, true
	case isMarkerLine(line, c.ownEnd):
		return false, true
	default:
		return inOwn, inOwn
	}
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

	arguments := []string{}
	for field := range strings.FieldsSeq(rest) {
		arguments = append(arguments, strings.Trim(field, "\""))
	}

	return arguments
}

package main

import (
	"fmt"
	"path"
	"strings"
)

const (
	selectionModeFull = "full"
	selectionModePR   = "pr"
)

type selectionOptions struct {
	secretAvailable bool
	mode            string
	changedFiles    []string
}

type globMatcher struct {
	patterns          []string
	files             []string
	patternIndex      int
	fileIndex         int
	globstarIndex     int
	globstarFileIndex int
}

func normalizeSelectionPath(value string) string {
	return path.Clean(strings.ReplaceAll(value, "\\", "/"))
}

func validateGlobs(patterns []string) error {
	for _, pattern := range patterns {
		normalized := normalizeSelectionPath(pattern)
		if strings.TrimSpace(pattern) == "" || normalized == "." || path.IsAbs(normalized) ||
			normalized == ".." || strings.HasPrefix(normalized, "../") {
			return fmt.Errorf(
				"%w: %q must be a non-empty repository-relative pattern",
				errInvalidGlob,
				pattern,
			)
		}
		if _, err := path.Match(strings.ReplaceAll(pattern, "\\", "/"), ""); err != nil {
			return fmt.Errorf("%w: %q: %w", errInvalidGlob, pattern, err)
		}
	}
	return nil
}

func matchGlob(pattern, filePath string) bool {
	filePath = normalizeSelectionPath(filePath)
	if filePath == "." || path.IsAbs(filePath) || filePath == ".." ||
		strings.HasPrefix(filePath, "../") {
		return false
	}
	matcher := globMatcher{
		patterns:      strings.Split(normalizeSelectionPath(pattern), "/"),
		files:         strings.Split(filePath, "/"),
		globstarIndex: -1,
	}
	return matcher.match()
}

func (m *globMatcher) match() bool {
	for m.fileIndex < len(m.files) {
		if m.matchSegment() {
			continue
		}
		if !m.backtrack() {
			return false
		}
	}
	for m.patternIndex < len(m.patterns) && m.patterns[m.patternIndex] == "**" {
		m.patternIndex++
	}
	return m.patternIndex == len(m.patterns)
}

func (m *globMatcher) matchSegment() bool {
	if m.patternIndex == len(m.patterns) {
		return false
	}
	if m.patterns[m.patternIndex] == "**" {
		m.globstarIndex = m.patternIndex
		m.globstarFileIndex = m.fileIndex
		m.patternIndex++
		return true
	}
	matched, err := path.Match(m.patterns[m.patternIndex], m.files[m.fileIndex])
	if err != nil || !matched {
		return false
	}
	m.patternIndex++
	m.fileIndex++
	return true
}

func (m *globMatcher) backtrack() bool {
	if m.globstarIndex < 0 {
		return false
	}
	m.globstarFileIndex++
	m.fileIndex = m.globstarFileIndex
	m.patternIndex = m.globstarIndex + 1
	return true
}

func matchesAny(changedFiles, patterns []string) bool {
	for _, file := range changedFiles {
		for _, pattern := range patterns {
			if matchGlob(pattern, file) {
				return true
			}
		}
	}
	return false
}

func selectSuites(m *Manifest, group string, opts selectionOptions) []Suite {
	suites := m.SuitesIn(group)
	selected := make([]Suite, 0, len(suites))
	full, coreChanged := opts.scope(m.Selection)
	for _, s := range suites {
		if s.RequiresSecret && !opts.secretAvailable {
			continue
		}
		if opts.includes(s, full, coreChanged) {
			selected = append(selected, s)
		}
	}
	return selected
}

func (opts selectionOptions) scope(config SelectionConfig) (bool, bool) {
	if opts.mode == "" || opts.mode == selectionModeFull {
		return true, false
	}
	if opts.mode != selectionModePR || len(opts.changedFiles) == 0 {
		return false, false
	}
	if matchesAny(opts.changedFiles, config.FullRunPaths) {
		return true, false
	}
	return false, matchesAny(opts.changedFiles, config.CorePaths)
}

func (opts selectionOptions) includes(s Suite, full, coreChanged bool) bool {
	return full ||
		(opts.mode == selectionModePR && ((coreChanged && s.PRSmoke) || matchesAny(opts.changedFiles, s.Paths)))
}

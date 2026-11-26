package git

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const gitConfigCountPrefix = "GIT_CONFIG_COUNT="

// AppendGitConfig adds a Git configuration key/value pair to baseEnv using the
// GIT_CONFIG_COUNT, GIT_CONFIG_KEY_<n>, and GIT_CONFIG_VALUE_<n> environment variables.
// It inspects baseEnv and the process environment (os.Environ()) to find the next
// available index, preserving existing entries.
func AppendGitConfig(baseEnv []string, key, value string) []string {
	return AppendGitConfigWithEnviron(baseEnv, os.Environ(), key, value)
}

// AppendGitConfigWithEnviron adds a Git configuration key/value pair to baseEnv,
// determining the next configuration index from baseEnv and the provided environ slice.
func AppendGitConfigWithEnviron(baseEnv, environ []string, key, value string) []string {
	nextIndex := nextGitConfigIndex(baseEnv, environ)
	newCount := nextIndex + 1

	out := make([]string, 0, len(baseEnv)+3)
	for _, entry := range baseEnv {
		if strings.HasPrefix(entry, gitConfigCountPrefix) {
			continue
		}
		out = append(out, entry)
	}

	return append(out,
		fmt.Sprintf("%s%d", gitConfigCountPrefix, newCount),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", nextIndex, key),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", nextIndex, value),
	)
}

func nextGitConfigIndex(baseEnv, environ []string) int {
	parsedCount := parseGitConfigCount(baseEnv)
	if parsedCount < 0 {
		parsedCount = parseGitConfigCount(environ)
	}

	maxIndex := -1
	scanMaxKeyIndex(environ, &maxIndex)
	scanMaxKeyIndex(baseEnv, &maxIndex)

	if parsedCount > maxIndex+1 {
		return parsedCount
	}
	if maxIndex >= 0 {
		return maxIndex + 1
	}
	if parsedCount >= 0 {
		return parsedCount
	}
	return 0
}

func parseGitConfigCount(env []string) int {
	for _, entry := range env {
		if val, ok := strings.CutPrefix(entry, gitConfigCountPrefix); ok {
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				return n
			}
			return -1
		}
	}
	return -1
}

func scanMaxKeyIndex(env []string, maxIndex *int) {
	const keyPrefix = "GIT_CONFIG_KEY_"
	for _, entry := range env {
		if val, ok := strings.CutPrefix(entry, keyPrefix); ok {
			idxStr, _, hasEqual := strings.Cut(val, "=")
			if !hasEqual {
				continue
			}
			if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 0 {
				if idx > *maxIndex {
					*maxIndex = idx
				}
			}
		}
	}
}

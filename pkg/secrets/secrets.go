package secrets

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ParseSecretsFile reads a dotenv-style secrets file and returns the key-value
// pairs as a map. Lines starting with # are comments, blank lines are ignored,
// and values are split on the first = only (values may contain =). Whitespace
// around keys is trimmed.
func ParseSecretsFile(path string) (map[string]string, error) {
	f, err := os.Open(path) // #nosec G304 -- User-specified secrets file path is intentional.
	if err != nil {
		return nil, fmt.Errorf("open secrets file: %w", err)
	}
	defer func() { _ = f.Close() }()

	secrets := map[string]string{}
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip blank lines and comments.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("secrets file %s: line %d: missing '=' separator", path, lineNum)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("secrets file %s: line %d: empty key", path, lineNum)
		}

		secrets[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read secrets file %s: %w", path, err)
	}

	return secrets, nil
}

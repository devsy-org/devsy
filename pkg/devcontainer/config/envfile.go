package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// ParseKeyValueFile reads an env-style file and returns its "KEY=VALUE" lines.
// Blank lines and lines beginning with '#' are skipped. It errors on invalid
// UTF-8, keys that are empty or contain spaces, and empty values.
func ParseKeyValueFile(filename string) ([]string, error) {
	f, err := os.Open(filename) // #nosec G304 -- caller-provided env file path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var keyValuePairs []string
	scanner := bufio.NewScanner(f)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line, keep, err := parseEnvLine(filename, lineNum, scanner.Bytes())
		if err != nil {
			return nil, err
		}
		if keep {
			keyValuePairs = append(keyValuePairs, line)
		}
	}
	return keyValuePairs, nil
}

// parseEnvLine validates a single env-file line. keep reports whether the line
// is a real "KEY=VALUE" entry (blank and comment lines are skipped with keep=false).
func parseEnvLine(filename string, lineNum int, raw []byte) (line string, keep bool, err error) {
	if !utf8.Valid(raw) {
		return "", false, fmt.Errorf(
			"env file %s contains invalid utf8 bytes in line %d", filename, lineNum,
		)
	}

	line = string(raw)
	if len(line) == 0 || strings.HasPrefix(line, "#") {
		return "", false, nil
	}

	key, value, found := strings.Cut(line, "=")
	if len(key) == 0 || strings.Contains(key, " ") {
		return "", false, fmt.Errorf(
			"env file %s contains invalid variable key in line %d: %s", filename, lineNum, line,
		)
	}
	if len(value) == 0 {
		return "", false, fmt.Errorf(
			"env file %s contains invalid variable value in line %d: %s", filename, lineNum, line,
		)
	}
	return line, found, nil
}

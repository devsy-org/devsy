package buildkit

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
)

// SplitBuildSecret parses one "NAME=VALUE" build-secret entry. The error never
// echoes entry, which may hold the secret value (e.g. "=value").
func SplitBuildSecret(index int, entry string) (name, value string, err error) {
	name, value, ok := strings.Cut(entry, "=")
	if !ok || name == "" {
		return "", "", fmt.Errorf("invalid build secret at index %d, expected NAME=VALUE", index)
	}
	return name, value, nil
}

func ParseBuildSecrets(entries []string) (map[string][]byte, error) {
	secrets := make(map[string][]byte, len(entries))
	for i, entry := range entries {
		name, value, err := SplitBuildSecret(i, entry)
		if err != nil {
			return nil, err
		}
		secrets[name] = []byte(value)
	}
	return secrets, nil
}

func buildSecretsAttachable(entries []string) (session.Attachable, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	secrets, err := ParseBuildSecrets(entries)
	if err != nil {
		return nil, err
	}
	return secretsprovider.FromMap(secrets), nil
}

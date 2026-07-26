package secrets

import (
	"encoding/json"
	"fmt"
	"os"
)

func ParseSecretsFile(path string) (map[string]string, error) {
	// #nosec G304 -- User-specified secrets file path is intentional.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read secrets file: %w", err)
	}

	secrets := map[string]string{}
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parse secrets file %s: %w", path, err)
	}
	if _, ok := secrets[""]; ok {
		return nil, fmt.Errorf("parse secrets file %s: empty key not allowed", path)
	}

	return secrets, nil
}

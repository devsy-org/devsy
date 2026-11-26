package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// requireID returns the "<kind> is empty" error used by the Get*Dir helpers
// when an identifier is missing.
func requireID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("%s is empty", kind)
	}

	return nil
}

// saveJSON creates dir and atomically writes v as JSON to dir/file.
func saveJSON(dir, file string, v any, perm os.FileMode) error {
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return writeJSONFile(filepath.Join(dir, file), v, perm)
}

// writeJSONFile atomically writes v as JSON to path. The parent dir must exist.
func writeJSONFile(path string, v any, perm os.FileMode) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return WriteFileAtomic(path, data, perm)
}

// loadJSON reads and decodes the JSON file at path. Read errors are returned
// unwrapped so callers can test them with os.IsNotExist.
func loadJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	v := new(T)
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}

	return v, nil
}

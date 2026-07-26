package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
)

// fileBackend stores all values in one age-encrypted file, re-encrypting the
// whole map on each write.
type fileBackend struct {
	path string
	key  *fileKey
}

func newFileBackend(path string, key *fileKey) *fileBackend {
	return &fileBackend{path: path, key: key}
}

func (f *fileBackend) load() (map[string]string, error) {
	raw, err := os.ReadFile(f.path) // #nosec G304 -- path derived from config dir.
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}

		return nil, fmt.Errorf("read secrets file: %w", err)
	}

	reader, err := age.Decrypt(bytes.NewReader(raw), f.key.identity)
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets file: %w", err)
	}

	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read decrypted secrets: %w", err)
	}

	values := map[string]string{}
	if err := json.Unmarshal(plaintext, &values); err != nil {
		return nil, fmt.Errorf("parse secrets file: %w", err)
	}

	return values, nil
}

func (f *fileBackend) store(values map[string]string) error {
	plaintext, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}

	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, f.key.recipient)
	if err != nil {
		return fmt.Errorf("encrypt secrets: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("encrypt secrets: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalize encrypted secrets: %w", err)
	}

	return atomicWriteFile(f.path, buf.Bytes(), 0o600)
}

func (f *fileBackend) set(key, value string) error {
	values, err := f.load()
	if err != nil {
		return err
	}
	values[key] = value

	return f.store(values)
}

func (f *fileBackend) get(key string) (string, error) {
	values, err := f.load()
	if err != nil {
		return "", err
	}
	value, ok := values[key]
	if !ok {
		return "", ErrSecretNotFound
	}

	return value, nil
}

func (f *fileBackend) remove(key string) error {
	values, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := values[key]; !ok {
		return nil
	}
	delete(values, key)

	return f.store(values)
}

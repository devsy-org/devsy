package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/getsops/sops/v3/decrypt"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const (
	SOPSFormatter    = "sops"
	SOPSFormatYAML   = "yaml"
	SOPSFormatJSON   = "json"
	SOPSFormatDotenv = "dotenv"
)

// SOPSSource resolves values from one SOPS-encrypted document. A source is
// command-scoped: decrypted values are cached in memory for the lifetime of the
// source instance and are never persisted by Devsy.
type SOPSSource struct {
	name      string
	path      string
	format    string
	encrypted []byte

	once sync.Once
	data map[string]string
	err  error
}

func NewSOPSSource(name, filePath, format string) *SOPSSource {
	return &SOPSSource{name: name, path: filePath, format: format}
}

// NewSOPSDataSource is used for repository inspection where the encrypted file
// is read directly from a Git revision without being materialized on disk.
func NewSOPSDataSource(name, logicalPath, format string, encrypted []byte) *SOPSSource {
	return &SOPSSource{
		name:      name,
		path:      logicalPath,
		format:    format,
		encrypted: append([]byte(nil), encrypted...),
	}
}

func (s *SOPSSource) Get(ctx context.Context, name string) (ResolvedSecret, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedSecret{}, err
	}
	if s == nil {
		return ResolvedSecret{}, fmt.Errorf("SOPS secret source is nil")
	}
	s.once.Do(func() { s.data, s.err = s.load(ctx) })
	if s.err != nil {
		return ResolvedSecret{}, s.err
	}
	value, ok := s.data[name]
	if !ok {
		return ResolvedSecret{}, fmt.Errorf(
			"secret %q was not found in SOPS source %q",
			name,
			s.name,
		)
	}
	return ResolvedSecret{Name: name, Value: value, Sensitive: true, Source: s.name}, nil
}

// Validate forces decryption and document validation without exposing values.
func (s *SOPSSource) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.once.Do(func() { s.data, s.err = s.load(ctx) })
	return s.err
}

func (s *SOPSSource) load(ctx context.Context) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format, err := normalizeSOPSFormat(s.format, s.path)
	if err != nil {
		return nil, err
	}
	plaintext, err := s.decrypt(format)
	if err != nil {
		return nil, err
	}
	values, err := parseSOPSDocument(plaintext, format)
	if err != nil {
		return nil, fmt.Errorf("failed to load SOPS source %q: %w", s.name, err)
	}
	return values, nil
}

func (s *SOPSSource) decrypt(format string) ([]byte, error) {
	var (
		plaintext []byte
		err       error
	)
	if s.encrypted != nil {
		plaintext, err = decrypt.Data(s.encrypted, format)
	} else {
		if s.path == "" {
			return nil, fmt.Errorf("SOPS source %q has no path", s.name)
		}
		if _, statErr := os.Stat(s.path); statErr != nil {
			return nil, fmt.Errorf("read SOPS source %q: %w", s.name, statErr)
		}
		plaintext, err = decrypt.File(s.path, format)
	}
	if err != nil {
		// Do not include encrypted/decrypted document contents in the error. The
		// upstream error is retained because it contains actionable key-provider
		// information (for example, an unmatched age recipient or KMS failure).
		return nil, fmt.Errorf("failed to decrypt SOPS source %q: %w", s.name, err)
	}
	return plaintext, nil
}

func normalizeSOPSFormat(explicit, filePath string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(explicit))
	if format == "" {
		return inferSOPSFormat(filePath)
	}
	return canonicalSOPSFormat(format)
}

func inferSOPSFormat(filePath string) (string, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".yaml", ".yml":
		return SOPSFormatYAML, nil
	case ".json":
		return SOPSFormatJSON, nil
	case ".env":
		return SOPSFormatDotenv, nil
	default:
		return "", fmt.Errorf(
			"cannot determine SOPS format for %q; set format to yaml, json, or dotenv",
			filePath,
		)
	}
}

func canonicalSOPSFormat(format string) (string, error) {
	switch format {
	case SOPSFormatYAML, SOPSFormatJSON, SOPSFormatDotenv:
		return format, nil
	case "env":
		return SOPSFormatDotenv, nil
	default:
		return "", fmt.Errorf(
			"unsupported SOPS format %q; expected yaml, json, or dotenv",
			format,
		)
	}
}

func parseSOPSDocument(plaintext []byte, format string) (map[string]string, error) {
	if format == SOPSFormatDotenv {
		values, err := godotenv.Unmarshal(string(plaintext))
		if err != nil {
			return nil, fmt.Errorf("parse dotenv document: %w", err)
		}
		return values, nil
	}

	var raw map[string]any
	var err error
	if format == SOPSFormatJSON {
		err = json.Unmarshal(plaintext, &raw)
	} else {
		err = yaml.Unmarshal(plaintext, &raw)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s document: %w", format, err)
	}
	if raw == nil {
		return map[string]string{}, nil
	}

	out := make(map[string]string, len(raw))
	for key, value := range raw {
		scalar, err := stringifySecretScalar(value)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
		out[key] = scalar
	}
	return out, nil
}

func stringifySecretScalar(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool, int, int64, uint64, float64:
		return fmt.Sprint(v), nil
	case nil:
		return "", fmt.Errorf("value is null; only scalar non-null values are supported")
	default:
		return "", fmt.Errorf("value is not a scalar; nested objects and arrays are not supported")
	}
}

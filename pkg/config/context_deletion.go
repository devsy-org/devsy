package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	ContextDeletionIntentFile    = "context-deletion.json"
	maxContextDeletionIntentSize = 1024 * 1024
)

var (
	ErrContextDeletionPending       = errors.New("context deletion recovery required")
	ErrContextDeletionIntentInvalid = errors.New(
		"context deletion intent is invalid; preserve the intent file and repair it before retrying",
	)
	deletionValueName            = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	contextDeletionControlName   = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	contextDeletionWindowsDevice = regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
	contextNamePattern           = regexp.MustCompile(`^[a-z0-9-]+$`)
)

type ContextDeletionSecret struct {
	Name    string `json:"name"`
	Backend string `json:"backend"`
}

// ContextDeletionIntent stores only identifiers for an authorized forward
// deletion. Values and unlock credentials must never be written here.
type ContextDeletionIntent struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	ConfigFile     string                  `json:"configFile"`
	StoreDirectory string                  `json:"storeDirectory"`
	DataDirectory  string                  `json:"dataDirectory"`
	Context        string                  `json:"context"`
	Fingerprint    string                  `json:"fingerprint"`
	Secrets        []ContextDeletionSecret `json:"secrets"`
	EnvNames       []string                `json:"envNames"`
}

type ContextDeletionPendingError struct {
	Context string
	Message string
}

func (e *ContextDeletionPendingError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf(
		"context %q deletion is incomplete; retry devsy context delete %q with access to its original secret backends",
		e.Context,
		e.Context,
	)
}
func (e *ContextDeletionPendingError) Unwrap() error { return ErrContextDeletionPending }

// CheckPendingContextDeletion is startup detection only. It does not open a
// value store, request credentials, or change any persistent data.
func CheckPendingContextDeletion() error {
	intent, err := ReadContextDeletionIntent()
	if err != nil {
		return err
	}
	if intent != nil {
		return &ContextDeletionPendingError{Context: intent.Context}
	}
	return nil
}

func ContextDeletionFingerprint(ctx *ContextConfig) (string, error) {
	encoded, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func ReadContextDeletionIntent() (*ContextDeletionIntent, error) {
	path, _, err := contextDeletionPaths()
	if err != nil {
		return nil, ErrContextDeletionIntentInvalid
	}
	raw, err := readContextDeletionBytes(path)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	return decodeContextDeletionIntent(raw)
}

func readContextDeletionBytes(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrContextDeletionIntentInvalid
	}
	if !info.Mode().IsRegular() || info.Size() > maxContextDeletionIntentSize {
		return nil, ErrContextDeletionIntentInvalid
	}
	// #nosec G304 -- fixed intent filename under the current config directory.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrContextDeletionIntentInvalid
	}
	return raw, nil
}

func decodeContextDeletionIntent(raw []byte) (*ContextDeletionIntent, error) {
	var intent ContextDeletionIntent
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return nil, ErrContextDeletionIntentInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ErrContextDeletionIntentInvalid
	}
	if err := validateContextDeletionIntent(&intent); err != nil {
		return nil, err
	}
	return &intent, nil
}

func WriteContextDeletionIntent(intent *ContextDeletionIntent) error {
	return writeContextDeletionIntent(intent, syncContextDeletionDirectory)
}

func writeContextDeletionIntent(
	intent *ContextDeletionIntent,
	syncDirectory func(string) error,
) error {
	if err := validateContextDeletionIntent(intent); err != nil {
		return err
	}
	path, _, err := contextDeletionPaths()
	if err != nil {
		return ErrContextDeletionIntentInvalid
	}
	encoded, err := json.Marshal(intent)
	if err != nil || len(encoded) > maxContextDeletionIntentSize {
		return ErrContextDeletionIntentInvalid
	}
	if err := writeContextDeletionAtomicWithSync(path, encoded, syncDirectory); err != nil {
		err = contextDeletionWriteFailure(path, encoded, syncDirectory, err)
		if errors.Is(err, ErrContextDeletionPending) {
			return &ContextDeletionPendingError{
				Context: intent.Context,
				Message: fmt.Sprintf(
					"context %q deletion intent could not be durably canceled; retry devsy context delete %q to complete deletion",
					intent.Context,
					intent.Context,
				),
			}
		}
		return err
	}
	return nil
}

func contextDeletionWriteFailure(
	path string,
	encoded []byte,
	syncDirectory func(string) error,
	err error,
) error {
	var writeErr *contextDeletionWriteError
	if errors.As(err, &writeErr) && writeErr.installed {
		if cancelErr := cancelContextDeletionIntent(
			path,
			encoded,
			syncDirectory,
		); cancelErr != nil {
			return ErrContextDeletionPending
		}
	}
	return errors.New("could not durably write context deletion intent; no values were deleted")
}

type contextDeletionWriteError struct {
	installed bool
	cause     error
}

func (e *contextDeletionWriteError) Error() string { return "context deletion intent write failed" }
func (e *contextDeletionWriteError) Unwrap() error { return e.cause }

func cancelContextDeletionIntent(path string, raw []byte, syncDirectory func(string) error) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		_ = writeContextDeletionAtomicWithSync(path, raw, syncDirectory)
		return err
	}
	return nil
}

func ClearContextDeletionIntent() error {
	path, _, err := contextDeletionPaths()
	if err != nil {
		return ErrContextDeletionIntentInvalid
	}
	// Read first so an unsuccessful directory fsync can recreate the marker.
	// #nosec G304 -- fixed intent filename under the current config directory.
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New(
			"could not clear context deletion intent; retry the original context deletion",
		)
	}
	if err := os.Remove(path); err != nil {
		return errors.New(
			"could not clear context deletion intent; retry the original context deletion",
		)
	}
	if err := syncContextDeletionDirectory(filepath.Dir(path)); err != nil {
		_ = writeContextDeletionAtomic(path, raw)
		return errors.New(
			"could not durably clear context deletion intent; retry the original context deletion",
		)
	}
	return nil
}

// ContextDeletionDataDirectory binds context-directory cleanup to the original
// data root without resolving the final context directory or following its link.
func ContextDeletionDataDirectory() (string, error) {
	path, err := DefaultPathManager().DataDir()
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// ContextDeletionStoreDirectory identifies the configured managed-value root.
// It can differ from the real config file's parent when the file is a symlink.
func ContextDeletionStoreDirectory() (string, error) {
	path, err := GetConfigPath()
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(path), nil
	}
	return "", err
}

func contextDeletionPaths() (string, string, error) {
	path, err := getConfigMutationPath()
	if err != nil {
		return "", "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	resolved, resolveErr := filepath.EvalSymlinks(path)
	if resolveErr == nil {
		path = resolved
	} else if !errors.Is(resolveErr, os.ErrNotExist) {
		return "", "", resolveErr
	}
	return filepath.Join(filepath.Dir(path), ContextDeletionIntentFile), filepath.Clean(path), nil
}

func validateContextDeletionIntent(intent *ContextDeletionIntent) error {
	if intent == nil || intent.SchemaVersion != 1 {
		return ErrContextDeletionIntentInvalid
	}
	if !validDeletionContextName(intent.Context) {
		return ErrContextDeletionIntentInvalid
	}
	if !validDeletionFingerprint(intent.Fingerprint) || !validDeletionNames(intent) {
		return ErrContextDeletionIntentInvalid
	}
	return validateContextDeletionRoots(intent)
}

func validDeletionFingerprint(fingerprint string) bool {
	hash, err := hex.DecodeString(fingerprint)
	return err == nil && len(hash) == sha256.Size
}

func validateContextDeletionRoots(intent *ContextDeletionIntent) error {
	_, expected, err := contextDeletionPaths()
	if err != nil || intent.ConfigFile != expected {
		return ErrContextDeletionIntentInvalid
	}
	directory, err := ContextDeletionStoreDirectory()
	if err != nil || intent.StoreDirectory != directory {
		return ErrContextDeletionIntentInvalid
	}
	dataDirectory, err := ContextDeletionDataDirectory()
	if err != nil || intent.DataDirectory != dataDirectory {
		return ErrContextDeletionIntentInvalid
	}
	return nil
}

// Keep safe legacy names recoverable, including device names on POSIX.
func validDeletionContextName(name string) bool {
	return name != DefaultContext && !invalidContextPathName(name) &&
		(runtime.GOOS != "windows" || !reservedWindowsContextName(name))
}

// ValidateContextName applies portable creation rules so a new context can be
// deleted safely on every supported platform.
func ValidateContextName(name string) error {
	if !contextNamePattern.MatchString(name) {
		return errors.New("context name can only include lower case letters, numbers or dashes")
	}
	if len(name) > 48 {
		return errors.New("context name cannot be longer than 48 characters")
	}
	if reservedWindowsContextName(name) {
		return errors.New("context name cannot be a reserved Windows device name")
	}
	return nil
}

func invalidContextPathName(name string) bool {
	return name == "" || name == "." || name == ".." || strings.HasSuffix(name, ".") ||
		strings.HasSuffix(name, " ") ||
		strings.ContainsAny(name, `/\:<>"|?*`) ||
		contextDeletionControlName.MatchString(name)
}

func reservedWindowsContextName(name string) bool {
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	return contextDeletionWindowsDevice.MatchString(base)
}

func validDeletionNames(intent *ContextDeletionIntent) bool {
	for _, name := range intent.EnvNames {
		if !deletionValueName.MatchString(name) {
			return false
		}
	}
	for _, secret := range intent.Secrets {
		if !deletionValueName.MatchString(secret.Name) {
			return false
		}
		if secret.Backend != "file" && secret.Backend != "keyring" {
			return false
		}
	}
	return true
}

func writeContextDeletionAtomic(path string, raw []byte) error {
	return writeContextDeletionAtomicWithSync(path, raw, syncContextDeletionDirectory)
}

func writeContextDeletionAtomicWithSync(
	path string,
	raw []byte,
	syncDirectory func(string) error,
) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &contextDeletionWriteError{cause: err}
	}
	temp, err := createTempConfig(dir, ContextDeletionIntentFile, raw)
	if err != nil {
		return &contextDeletionWriteError{cause: err}
	}
	// #nosec G703 -- temp was created beside the validated current config file.
	defer func() { _ = os.Remove(temp) }()
	// #nosec G703 -- fixed intent filename and temp under the current config directory.
	if err := os.Rename(temp, path); err != nil {
		return &contextDeletionWriteError{cause: err}
	}
	if err := syncDirectory(dir); err != nil {
		return &contextDeletionWriteError{installed: true, cause: err}
	}
	return nil
}

func syncContextDeletionDirectory(dir string) error {
	// #nosec G304 -- directory is derived from the current config path.
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	err = file.Sync()
	if runtime.GOOS == "windows" || errors.Is(err, os.ErrInvalid) {
		return nil
	}
	return err
}

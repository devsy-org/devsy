package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const (
	EnvPassphraseFile     = "DEVSY_SECRETS_PASSPHRASE_FILE" // #nosec G101 -- environment variable name.
	MaxPassphraseFileSize = 64 * 1024
	keyringPassphraseUser = "__file_passphrase__" // #nosec G101 -- keyring account name.
)

type PassphraseSource string

const (
	PassphraseExplicit   PassphraseSource = "explicit"
	PassphraseEnv        PassphraseSource = "environment"
	PassphraseFile       PassphraseSource = "file"
	PassphraseRemembered PassphraseSource = "keyring"
	PassphrasePrompt     PassphraseSource = "prompt"
)

type UnlockMaterial struct {
	Passphrase string
	Source     PassphraseSource
}
type UnlockRequest struct {
	AllowPrompt bool
	Purpose     string
}
type UnlockMaterialResolver interface {
	ResolvePassphrase(context.Context, UnlockRequest) (UnlockMaterial, error)
}
type UnlockMaterialResolverFunc func(context.Context, UnlockRequest) (UnlockMaterial, error)

func (f UnlockMaterialResolverFunc) ResolvePassphrase(
	ctx context.Context,
	r UnlockRequest,
) (UnlockMaterial, error) {
	return f(ctx, r)
}

// DefaultUnlockResolver chooses one credential source. Decryption failure never
// falls through to another source. Prompt callbacks belong to the invoking UI.
type DefaultUnlockResolver struct {
	ExplicitPassphrase string
	LookupEnv          func(string) string
	ReadRemembered     func() (string, error)
	Prompt             func(context.Context, UnlockRequest) (string, error)
}

func (r DefaultUnlockResolver) ResolvePassphrase(
	ctx context.Context,
	request UnlockRequest,
) (UnlockMaterial, error) {
	if r.ExplicitPassphrase != "" {
		return UnlockMaterial{r.ExplicitPassphrase, PassphraseExplicit}, nil
	}
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.Getenv
	}
	if value := lookup(EnvPassphrase); value != "" {
		return UnlockMaterial{value, PassphraseEnv}, nil
	}
	// Credential files and remembered unlock material only apply to an
	// initialized passphrase store; they never opt a new store into protection.
	if request.Purpose == "initialize" {
		return UnlockMaterial{}, &UnlockRequiredError{Backend: BackendFile}
	}
	if path := lookup(EnvPassphraseFile); path != "" {
		value, err := readPassphraseFile(path)
		if err != nil {
			return UnlockMaterial{}, err
		}
		return UnlockMaterial{value, PassphraseFile}, nil
	}
	return r.resolveFallback(ctx, request)
}

func (r DefaultUnlockResolver) resolveFallback(
	ctx context.Context,
	request UnlockRequest,
) (UnlockMaterial, error) {
	remembered := r.ReadRemembered
	if remembered == nil {
		remembered = ReadRememberedPassphrase
	}
	if value, err := remembered(); err == nil && value != "" {
		return UnlockMaterial{value, PassphraseRemembered}, nil
	}
	if request.AllowPrompt && r.Prompt != nil {
		value, err := r.Prompt(ctx, request)
		if err != nil {
			return UnlockMaterial{}, err
		}
		if value != "" {
			return UnlockMaterial{value, PassphrasePrompt}, nil
		}
	}
	return UnlockMaterial{}, &UnlockRequiredError{Backend: BackendFile}
}

func preflightPassphraseFile(path string) error {
	// #nosec G703 -- caller explicitly selects this credential file; type, permissions and size are validated below.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect passphrase file: %w", err)
	}
	return validatePassphraseFile(info)
}

func readPassphraseFile(path string) (string, error) {
	if err := preflightPassphraseFile(path); err != nil {
		return "", err
	}
	f, err := os.Open(path) // #nosec G304 -- explicitly supplied credential file path.
	if err != nil {
		return "", fmt.Errorf("open passphrase file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect passphrase file: %w", err)
	}
	if err := validatePassphraseFile(info); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxPassphraseFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read passphrase file: %w", err)
	}
	return decodePassphraseFile(raw)
}

func decodePassphraseFile(raw []byte) (string, error) {
	if len(raw) > MaxPassphraseFileSize {
		return "", errors.New("passphrase file exceeds maximum size")
	}
	value := strings.TrimSuffix(string(raw), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		value = strings.TrimSuffix(value, "\r")
	}
	if value == "" {
		return "", errors.New("passphrase file is empty")
	}
	return value, nil
}

func validatePassphraseFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("passphrase file must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New(
			"passphrase file permissions must restrict access to its owner " +
				"(chmod 600; use a restrictive projected secret file mode)",
		)
	}
	return nil
}

package credstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
)

const (
	tempPrefix   = ".gateway-credentials-"
	tempPattern  = tempPrefix + "*"
	maxFileBytes = 64 << 10
	goosWindows  = "windows"
)

func configDir() (string, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func (s *Store) filePath() (string, error) {
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// writeFile replaces the file in one step, so a reader never sees a partial
// token. The temporary file is created with mode 0600.
func (s *Store) writeFile(data []byte) (string, error) {
	path, err := s.filePath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create credential directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return "", fmt.Errorf("create credential file: %w", err)
	}
	if err := fillAndRename(tmp, data, path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

func fillAndRename(tmp *os.File, data []byte, path string) error {
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != goosWindows {
		_ = tmp.Close()
		return errors.New("set credential file mode")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.New("write credential file")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.New("write credential file")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("write credential file")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.New("replace credential file")
	}
	return nil
}

func (s *Store) loadFile() (Credential, error) {
	path, err := s.filePath()
	if err != nil {
		return Credential{}, err
	}
	data, err := readPrivateFile(path)
	if err != nil {
		return Credential{}, err
	}
	return decode(data)
}

// readPrivateFile reads a regular file that only its owner can access. It
// checks the open file, so the file cannot change between the check and the read.
func readPrivateFile(path string) ([]byte, error) {
	f, err := openPrivateFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		return nil, errors.New("read credential file")
	}
	return data, nil
}

func openPrivateFile(path string) (*os.File, error) {
	link, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, errors.New("read credential file")
	}
	// Opening a FIFO would block, so refuse anything else before Open.
	if !link.Mode().IsRegular() {
		return nil, fmt.Errorf("credential file %s is not a regular file", path)
	}
	// #nosec G304 -- the path is fixed beside the Devsy config file.
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("read credential file")
	}
	if err := checkPrivate(path, f, link); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func checkPrivate(path string, f *os.File, link os.FileInfo) error {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(link, info) {
		return fmt.Errorf("credential file %s is not a regular file", path)
	}
	if runtime.GOOS != goosWindows && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"credential file %s is readable by others; run devsy logout and log in again", path,
		)
	}
	return nil
}

// removeFile deletes the credential file and any temporary file a crashed
// write left behind. It reports whether the credential file existed.
func (s *Store) removeFile() (bool, error) {
	path, err := s.filePath()
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), tempPrefix) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("remove credential file")
	}
	return true, nil
}

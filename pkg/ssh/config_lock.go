package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/gofrs/flock"
)

const (
	configLockSuffix  = ".devsy.lock"
	maxSymlinkDepth   = 16
	defaultConfigMode = os.FileMode(0o600)
	configDirMode     = os.FileMode(0o755)
)

var configMutex sync.Mutex

func lockConfigFile(target string) (func(), error) {
	configMutex.Lock()

	err := os.MkdirAll(filepath.Dir(target), configDirMode)
	if err != nil {
		log.Debugf("error creating ssh directory: %v", err)
	}

	fileLock := flock.New(target + configLockSuffix)
	err = fileLock.Lock()
	if err != nil {
		configMutex.Unlock()
		return nil, fmt.Errorf("lock ssh config: %w", err)
	}

	return func() {
		_ = fileLock.Unlock()
		configMutex.Unlock()
	}, nil
}

func resolveSymlinkTarget(path string) (string, error) {
	current := path
	for range maxSymlinkDepth {
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return current, nil
			}

			return "", fmt.Errorf("stat ssh config: %w", err)
		}

		if info.Mode()&os.ModeSymlink == 0 {
			return current, nil
		}

		link, err := os.Readlink(current)
		if err != nil {
			return "", fmt.Errorf("read ssh config link: %w", err)
		}

		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(current), link)
		}
		current = link
	}

	return "", fmt.Errorf("resolve ssh config link %s: too many levels of symbolic links", path)
}

func existingConfigMode(target string) os.FileMode {
	info, err := os.Stat(target)
	if err != nil {
		return defaultConfigMode
	}

	return info.Mode().Perm()
}

func fillTempConfig(temp *os.File, data []byte, mode os.FileMode) error {
	_, err := temp.Write(data)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temp ssh config: %w", err)
	}

	err = temp.Chmod(mode)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temp ssh config: %w", err)
	}

	err = temp.Sync()
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temp ssh config: %w", err)
	}

	err = temp.Close()
	if err != nil {
		return fmt.Errorf("close temp ssh config: %w", err)
	}

	return nil
}

func writeFileAtomic(target string, data []byte) (retErr error) {
	dir := filepath.Dir(target)
	err := os.MkdirAll(dir, configDirMode)
	if err != nil {
		log.Debugf("error creating ssh directory: %v", err)
	}

	mode := existingConfigMode(target)

	temp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".devsy-*")
	if err != nil {
		return fmt.Errorf("create temp ssh config: %w", err)
	}

	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()

	err = fillTempConfig(temp, data, mode)
	if err != nil {
		return err
	}

	err = os.Rename(tempPath, target)
	if err != nil {
		return fmt.Errorf("replace ssh config: %w", err)
	}

	return nil
}

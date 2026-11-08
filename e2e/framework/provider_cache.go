package framework

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

const (
	providerDownloadCacheEnv = "DEVSY_E2E_PROVIDER_DOWNLOAD_CACHE"
	providerCacheHomePerm    = 0o700
	providerCacheDirPerm     = 0o750
	providerCacheFilePerm    = 0o755
)

var providerCacheRelativeDir = filepath.Join("cache", "providers")

func seedProviderDownloadCache() error {
	source := os.Getenv(providerDownloadCacheEnv)
	home := os.Getenv(config.EnvHome)
	if source == "" || home == "" {
		return nil
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", providerDownloadCacheEnv, err)
	}
	defer func() { _ = sourceRoot.Close() }()
	homeRoot, err := openHomeRoot(home)
	if err != nil {
		return fmt.Errorf("open %s: %w", config.EnvHome, err)
	}
	defer func() { _ = homeRoot.Close() }()
	if err := homeRoot.MkdirAll(providerCacheRelativeDir, providerCacheDirPerm); err != nil {
		return fmt.Errorf("create provider download cache: %w", err)
	}
	return copyMissingFiles(source, sourceRoot, homeRoot)
}

func openHomeRoot(home string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(home))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(home)
	if err := parent.Mkdir(
		name,
		providerCacheHomePerm,
	); err != nil &&
		!errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return parent.OpenRoot(name)
}

func copyMissingFiles(source string, sourceRoot, homeRoot *os.Root) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", providerDownloadCacheEnv, err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		destination := filepath.Join(providerCacheRelativeDir, entry.Name())
		if err := copyIfMissing(sourceRoot, entry.Name(), homeRoot, destination); err != nil {
			return fmt.Errorf("seed provider download cache: %w", err)
		}
	}
	return nil
}

func copyIfMissing(
	sourceRoot *os.Root, name string, destinationRoot *os.Root, destination string,
) (err error) {
	if _, statErr := destinationRoot.Stat(destination); statErr == nil {
		return nil
	}
	in, err := sourceRoot.Open(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	partial := fmt.Sprintf("%s.%d.%d.partial", destination, os.Getpid(), time.Now().UnixNano())
	out, err := destinationRoot.OpenFile(
		partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, providerCacheFilePerm,
	)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.Join(err, destinationRoot.Remove(partial))
	}
	if err := destinationRoot.Rename(partial, destination); err != nil {
		return errors.Join(err, destinationRoot.Remove(partial))
	}
	return nil
}

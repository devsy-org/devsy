package framework

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
)

const (
	providerDownloadCacheEnv = "DEVSY_E2E_PROVIDER_DOWNLOAD_CACHE"
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
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("open %s: %w", config.EnvHome, err)
	}
	defer func() { _ = homeRoot.Close() }()
	if err := homeRoot.MkdirAll(providerCacheRelativeDir, providerCacheDirPerm); err != nil {
		return fmt.Errorf("create provider download cache: %w", err)
	}
	return copyMissingFiles(source, sourceRoot, homeRoot)
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
	out, err := destinationRoot.OpenFile(
		destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, providerCacheFilePerm,
	)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}

package ide

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/extract"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
)

// DownloadAndExtract downloads the archive at url and extracts it into destDir.
// It stages into a sibling dir and swaps on success, so a failure leaves any
// pre-existing install intact.
func DownloadAndExtract(
	ctx context.Context,
	url, destDir string,
	opts ...extract.Option,
) error {
	tmpPath, err := downloadToTemp(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()

	stagingDir, err := extractToStaging(url, tmpPath, destDir, opts...)
	if err != nil {
		return err
	}
	return swapIntoPlace(stagingDir, destDir)
}

func downloadToTemp(ctx context.Context, url string) (string, error) {
	tmpFile, err := os.CreateTemp("", "devsy-ide-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()

	if err := devsyhttp.DownloadToFile(ctx, url, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func extractToStaging(
	url, tmpPath, destDir string,
	opts ...extract.Option,
) (string, error) {
	file, err := os.Open(filepath.Clean(tmpPath))
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	// #nosec G301 -- IDE install dir
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", fmt.Errorf("create install parent: %w", err)
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(destDir), "."+filepath.Base(destDir)+".staging.*")
	if err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	if err := extract.Extract(file, stagingDir, opts...); err != nil {
		cleanupStaging(stagingDir)
		return "", fmt.Errorf("extract %s: %w", url, err)
	}

	// MkdirTemp yields 0700; restore the 0755 install-dir mode.
	// #nosec G302 -- IDE install dir
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		cleanupStaging(stagingDir)
		return "", fmt.Errorf("set install dir mode: %w", err)
	}
	return stagingDir, nil
}

func cleanupStaging(stagingDir string) {
	if rmErr := os.RemoveAll(stagingDir); rmErr != nil {
		log.Warnf("cleanup staging dir: path=%s err=%v", stagingDir, rmErr)
	}
}

// swapIntoPlace replaces destDir with stagingDir, keeping the old contents
// until the swap succeeds.
func swapIntoPlace(stagingDir, destDir string) error {
	backupDir := stagingDir + ".old"
	hasBackup, err := stashExisting(destDir, backupDir)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return err
	}

	if err := os.Rename(stagingDir, destDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		if hasBackup {
			if restoreErr := os.Rename(backupDir, destDir); restoreErr != nil {
				log.Warnf("restore install dir: path=%s err=%v", destDir, restoreErr)
			}
		}
		return fmt.Errorf("install to %s: %w", destDir, err)
	}

	if hasBackup {
		if rmErr := os.RemoveAll(backupDir); rmErr != nil {
			log.Warnf("cleanup old install: path=%s err=%v", backupDir, rmErr)
		}
	}
	return nil
}

// stashExisting moves an existing destDir aside so it can be restored if the
// swap fails; it reports whether a backup was made.
func stashExisting(destDir, backupDir string) (bool, error) {
	_, err := os.Stat(destDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect install dir: %w", err)
	}
	if err := os.Rename(destDir, backupDir); err != nil {
		return false, fmt.Errorf("stage existing install: %w", err)
	}
	return true, nil
}

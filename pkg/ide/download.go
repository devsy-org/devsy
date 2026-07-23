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

// DownloadAndExtract downloads the archive at url to a temporary file, then
// extracts it into destDir. The archive is unpacked into a sibling staging
// directory and swapped into place only on success, so a failed download or
// extraction never destroys a pre-existing install.
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

// downloadToTemp downloads url into a temporary file and returns its path. The
// temporary file is removed on failure.
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

// extractToStaging unpacks the archive at tmpPath into a sibling staging
// directory of destDir and returns its path. The staging directory is removed
// if extraction fails.
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
		if rmErr := os.RemoveAll(stagingDir); rmErr != nil {
			log.Warnf("cleanup staging dir: path=%s err=%v", stagingDir, rmErr)
		}
		return "", fmt.Errorf("extract %s: %w", url, err)
	}
	return stagingDir, nil
}

// swapIntoPlace replaces destDir with stagingDir, preserving the previous
// contents until the swap succeeds so a rename failure cannot leave destDir
// missing.
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

// stashExisting moves an existing destDir aside to backupDir so it can be
// restored if the subsequent swap fails. It reports whether a backup was made.
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

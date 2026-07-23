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
// extracts it into destDir.
func DownloadAndExtract(
	ctx context.Context,
	url, destDir string,
	opts ...extract.Option,
) error {
	tmpFile, err := os.CreateTemp("", "devsy-ide-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := devsyhttp.DownloadToFile(ctx, url, tmpPath); err != nil {
		return err
	}

	file, err := os.Open(filepath.Clean(tmpPath))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	// Extract into a sibling staging directory and only swap it into destDir on
	// success, so a failed extraction never destroys a pre-existing install.
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return fmt.Errorf("create install parent: %w", err)
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(destDir), "."+filepath.Base(destDir)+".staging.*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	swapped := false
	defer func() {
		if !swapped {
			if rmErr := os.RemoveAll(stagingDir); rmErr != nil {
				log.Warnf("cleanup staging dir: path=%s err=%v", stagingDir, rmErr)
			}
		}
	}()

	if err := extract.Extract(file, stagingDir, opts...); err != nil {
		return fmt.Errorf("extract %s: %w", url, err)
	}

	return swapIntoPlace(stagingDir, destDir, &swapped)
}

// swapIntoPlace replaces destDir with stagingDir, preserving the previous
// contents until the swap succeeds so a rename failure cannot leave destDir
// missing.
func swapIntoPlace(stagingDir, destDir string, swapped *bool) error {
	backupDir := stagingDir + ".old"
	hasBackup := false
	if _, err := os.Stat(destDir); err == nil {
		if err := os.Rename(destDir, backupDir); err != nil {
			return fmt.Errorf("stage existing install: %w", err)
		}
		hasBackup = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect install dir: %w", err)
	}

	if err := os.Rename(stagingDir, destDir); err != nil {
		if hasBackup {
			if restoreErr := os.Rename(backupDir, destDir); restoreErr != nil {
				log.Warnf("restore install dir: path=%s err=%v", destDir, restoreErr)
			}
		}
		return fmt.Errorf("install to %s: %w", destDir, err)
	}
	*swapped = true

	if hasBackup {
		if rmErr := os.RemoveAll(backupDir); rmErr != nil {
			log.Warnf("cleanup old install: path=%s err=%v", backupDir, rmErr)
		}
	}
	return nil
}

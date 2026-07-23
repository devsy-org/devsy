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

	if err := extract.Extract(file, destDir, opts...); err != nil {
		if rmErr := os.RemoveAll(destDir); rmErr != nil {
			log.Warnf("cleanup partial install: path=%s err=%v", destDir, rmErr)
		}
		return fmt.Errorf("extract %s: %w", url, err)
	}
	return nil
}

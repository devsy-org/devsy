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
// extracts it into destDir. The download (with retry and transfer-completeness
// verification) is delegated to devsyhttp.DownloadToFile, so a truncated
// transfer surfaces as a clear download error rather than an opaque
// decompression EOF; an extract failure therefore means a genuinely corrupt
// archive. On extract failure the partially-populated destDir is removed so a
// retry starts clean. Extract options (e.g. StripLevels) are passed through.
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

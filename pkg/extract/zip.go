package extract

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxUnzipEntrySize bounds a single zip entry's uncompressed size, guarding
// against decompression-bomb archives.
var maxUnzipEntrySize int64 = 2 << 30 // 2 GiB

func UnzipFolder(source, destination string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}

	for _, f := range reader.File {
		err := unzipFile(f, destination)
		if err != nil {
			return err
		}
	}

	return nil
}

func unzipFile(f *zip.File, destination string) error {
	// #nosec G305 -- the HasPrefix check below is exactly this guard; gosec
	// can't verify it statically, but every write path is gated on it
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", filePath)
	}

	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	destinationFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = destinationFile.Close() }()
	return copyBoundedZipEntry(f, destinationFile)
}

// copyBoundedZipEntry copies f's decompressed content into dst, bounding
// the copy so a decompression-bomb entry cannot exhaust disk space
// regardless of what its header claims.
func copyBoundedZipEntry(f *zip.File, dst io.Writer) error {
	zippedFile, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = zippedFile.Close() }()

	written, err := io.CopyN(dst, zippedFile, maxUnzipEntrySize+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if written > maxUnzipEntrySize {
		return fmt.Errorf(
			"zip entry %s exceeds the %d byte uncompressed size limit",
			f.Name, maxUnzipEntrySize,
		)
	}
	return nil
}

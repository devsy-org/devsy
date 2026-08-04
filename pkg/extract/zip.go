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
// against decompression-bomb archives (a small file that expands to
// exhaust disk space). Provider binaries are at most a few hundred MB
// today; this leaves generous headroom while still catching a bomb, which
// typically inflates orders of magnitude further.
var maxUnzipEntrySize int64 = 2 << 30 // 2 GiB

func UnzipFolder(source, destination string) error {
	// 1. Open the zip file
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	// 2. Get the absolute destination path
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}

	// 3. Iterate over zip files inside the archive and unzip each of them
	for _, f := range reader.File {
		err := unzipFile(f, destination)
		if err != nil {
			return err
		}
	}

	return nil
}

func unzipFile(f *zip.File, destination string) error {
	// 4. Check if file paths are not vulnerable to Zip Slip
	// #nosec G305 -- the HasPrefix check below is exactly this guard; gosec
	// can't verify it statically, but every write path is gated on it
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", filePath)
	}

	// 5. Create directory tree
	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	// 6. Create a destination file for unzipped content
	destinationFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = destinationFile.Close() }()

	// 7. Unzip the content of a file and copy it to the destination file
	return copyBoundedZipEntry(f, destinationFile)
}

// copyBoundedZipEntry copies f's decompressed content into dst, bounding
// the copy so a decompression-bomb entry can't exhaust disk space
// regardless of what its (attacker-controllable) header claims.
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

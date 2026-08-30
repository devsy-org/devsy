package extract

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func WriteTarExclude(
	writer io.Writer,
	localPath string,
	compress bool,
	excludedPaths []string,
) error {
	absolute, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("absolute: %w", err)
	}

	stat, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	gw := writer
	if compress {
		gwWriter := gzip.NewWriter(writer)
		defer func() { _ = gwWriter.Close() }()

		gw = gwWriter
	}

	tarWriter := tar.NewWriter(gw)
	defer func() { _ = tarWriter.Close() }()

	if !stat.IsDir() {
		return NewArchiver(
			filepath.Dir(absolute),
			tarWriter,
			excludedPaths,
		).AddToArchive(filepath.Base(absolute))
	}

	return NewArchiver(absolute, tarWriter, excludedPaths).AddToArchive("")
}

func WriteTar(writer io.Writer, localPath string, compress bool) error {
	return WriteTarExclude(writer, localPath, compress, nil)
}

// Archiver is responsible for compressing specific files and folders within a target directory.
type Archiver struct {
	basePath     string
	writer       *tar.Writer
	writtenFiles map[string]bool

	excludedPaths []string
}

// NewArchiver creates a new archiver.
func NewArchiver(basePath string, writer *tar.Writer, excludedPaths []string) *Archiver {
	return &Archiver{
		basePath:     basePath,
		writer:       writer,
		writtenFiles: map[string]bool{},

		excludedPaths: excludedPaths,
	}
}

// AddToArchive adds a new path to the archive.
func (a *Archiver) AddToArchive(relativePath string) error {
	if a.writtenFiles[relativePath] {
		return nil
	}

	stat, err := os.Lstat(path.Join(a.basePath, relativePath))
	if err != nil {
		return nil
	}

	if stat.IsDir() {
		if a.isExcluded(path.Clean(relativePath) + "/") {
			return nil
		}

		return a.tarFolder(relativePath, stat)
	}

	if a.isExcluded(path.Clean(relativePath)) {
		return nil
	}
	return a.tarFile(relativePath, stat)
}

func (a *Archiver) isExcluded(relativePath string) bool {
	for _, excludePath := range a.excludedPaths {
		if strings.HasPrefix(relativePath, excludePath) {
			return true
		}
	}

	return false
}

func (a *Archiver) tarFolder(target string, targetStat os.FileInfo) error {
	filePath := path.Join(a.basePath, target)
	files, err := os.ReadDir(filePath)
	if err != nil {
		return nil
	}

	if len(files) == 0 && target != "" {
		hdr, _ := tar.FileInfoHeader(targetStat, filePath)
		hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), targetStat)
		hdr.Name = target
		if err := a.writer.WriteHeader(hdr); err != nil {
			return fmt.Errorf("tar write header: %w", err)
		}
		a.writtenFiles[target] = true
	}

	for _, dirEntry := range files {
		f, err := dirEntry.Info()
		if err != nil {
			continue
		}

		if err = a.AddToArchive(path.Join(target, f.Name())); err != nil {
			return fmt.Errorf("recursive tar %s: %w", f.Name(), err)
		}
	}

	return nil
}

func (a *Archiver) tarFile(target string, targetStat os.FileInfo) error {
	var err error
	filepath := path.Join(a.basePath, target)

	// don't resolve symlinks
	linkName := ""
	if targetStat.Mode()&os.ModeSymlink == os.ModeSymlink {
		linkName, err = os.Readlink(filepath)
		if err != nil {
			return nil
		}
	}

	hdr, err := tar.FileInfoHeader(targetStat, linkName)
	if err != nil {
		return fmt.Errorf("create tar file info header: %w", err)
	}
	hdr.Name = target
	hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), targetStat)
	hdr.ModTime = time.Unix(targetStat.ModTime().Unix(), 0)

	if err := a.writer.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}

	// nothing more to do for non-regular
	if !targetStat.Mode().IsRegular() {
		a.writtenFiles[target] = true
		return nil
	}

	return a.writeRegularFileBody(target, filepath, targetStat)
}

func (a *Archiver) writeRegularFileBody(target, filePath string, targetStat os.FileInfo) error {
	// #nosec G304 -- path is derived from the archive being created, not external input
	f, err := os.Open(filePath)
	if err != nil {
		// We ignore open file and just treat it as okay
		return nil
	}
	defer func() { _ = f.Close() }()
	copied, err := io.CopyN(a.writer, f, targetStat.Size())
	if err != nil {
		return fmt.Errorf("tar copy file: %w", err)
	} else if copied != targetStat.Size() {
		return errors.New("tar: file truncated during read")
	}

	a.writtenFiles[target] = true
	return nil
}

const (
	modeISDIR  = 0o40000  // Directory
	modeISFIFO = 0o10000  // FIFO
	modeISREG  = 0o100000 // Regular file
	modeISLNK  = 0o120000 // Symbolic link
	modeISBLK  = 0o60000  // Block special file
	modeISCHR  = 0o20000  // Character special file
	modeISSOCK = 0o140000 // Socket
)

// chmodTarEntry is used to adjust the file permissions used in tar header based
// on the platform the archival is done.
func chmodTarEntry(perm os.FileMode) os.FileMode {
	if runtime.GOOS != "windows" {
		return perm
	}

	// perm &= 0755 // this 0-ed out tar flags (like link, regular file, directory marker etc.)
	permPart := perm & os.ModePerm
	noPermPart := perm &^ os.ModePerm
	// Add the x bit: make everything +x from windows
	permPart |= 0o111
	permPart &= 0o755

	return noPermPart | permPart
}

// fillGo18FileTypeBits fills type bits which have been removed on Go 1.9 archive/tar
// https://github.com/golang/go/commit/66b5a2f
func fillGo18FileTypeBits(mode int64, fi os.FileInfo) int64 {
	fm := fi.Mode()
	switch {
	case fm.IsRegular():
		mode |= modeISREG
	case fi.IsDir():
		mode |= modeISDIR
	case fm&os.ModeSymlink != 0:
		mode |= modeISLNK
	case fm&os.ModeDevice != 0:
		if fm&os.ModeCharDevice != 0 {
			mode |= modeISCHR
		} else {
			mode |= modeISBLK
		}
	case fm&os.ModeNamedPipe != 0:
		mode |= modeISFIFO
	case fm&os.ModeSocket != 0:
		mode |= modeISSOCK
	}
	return mode
}

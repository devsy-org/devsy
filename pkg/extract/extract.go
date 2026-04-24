package extract

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Options struct {
	StripLevels int

	Perm *os.FileMode
	UID  *int
	GID  *int
}

type Option func(o *Options)

func StripLevels(levels int) Option {
	return func(o *Options) {
		o.StripLevels = levels
	}
}

func Extract(origReader io.Reader, destFolder string, options ...Option) error {
	extractOptions := &Options{}
	for _, o := range options {
		o(extractOptions)
	}

	// read ahead
	bufioReader := bufio.NewReaderSize(origReader, 1024*1024)
	testBytes, err := bufioReader.Peek(2) // read 2 bytes
	if err != nil {
		return err
	}

	// is gzipped?
	var reader io.Reader
	if testBytes[0] == 31 && testBytes[1] == 139 {
		gzipReader, err := gzip.NewReader(bufioReader)
		if err != nil {
			return fmt.Errorf("error decompressing: %w", err)
		}
		defer func() { _ = gzipReader.Close() }()

		reader = gzipReader
	} else {
		reader = bufioReader
	}

	tarReader := tar.NewReader(reader)
	for {
		shouldContinue, err := extractNext(tarReader, destFolder, extractOptions)
		if err != nil {
			return fmt.Errorf("decompress: %w", err)
		} else if !shouldContinue {
			return nil
		}
	}
}

// withinDir checks that resolved stays inside the destFolder boundary.
func withinDir(resolved, destFolder string) bool {
	cleanDest := filepath.Clean(destFolder) + string(os.PathSeparator)
	return strings.HasPrefix(
		filepath.Clean(resolved)+string(os.PathSeparator),
		cleanDest,
	)
}

// resolveRelativePath strips levels and builds the output path.
func resolveRelativePath(header *tar.Header, opts *Options) string {
	rel := getRelativeFromFullPath("/"+header.Name, "")
	for i := 0; i < opts.StripLevels; i++ {
		rel = strings.TrimPrefix(rel, "/")
		idx := strings.Index(rel, "/")
		if idx == -1 {
			break
		}
		rel = rel[idx+1:]
	}
	if opts.StripLevels > 0 {
		rel = "/" + rel
	}
	return rel
}

func extractNext(
	tarReader *tar.Reader, destFolder string, options *Options,
) (bool, error) {
	header, err := tarReader.Next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("tar reader next: %w", err)
	}

	rel := resolveRelativePath(header, options)
	outFileName := filepath.Join(destFolder, rel)

	if !withinDir(outFileName, destFolder) {
		return false, fmt.Errorf(
			"path traversal detected: %s resolves outside destination",
			header.Name,
		)
	}

	switch header.Typeflag {
	case tar.TypeSymlink, tar.TypeLink:
		if err := validateLinkTarget(header, outFileName, destFolder); err != nil {
			return false, err
		}
	}

	if err := extractEntry(tarReader, header, outFileName, options); err != nil {
		return false, err
	}
	return true, nil
}

// validateLinkTarget ensures a symlink or hard link target stays within destFolder.
func validateLinkTarget(header *tar.Header, outFileName, destFolder string) error {
	linkTarget := resolveLinkTarget(header.Linkname, outFileName)
	if !withinDir(linkTarget, destFolder) {
		kind := "symlink"
		if header.Typeflag == tar.TypeLink {
			kind = "hard link"
		}
		return fmt.Errorf(
			"%s traversal detected: %s -> %s",
			kind, header.Name, header.Linkname,
		)
	}
	return nil
}

// resolveLinkTarget resolves a link target to an absolute path.
func resolveLinkTarget(linkname, outFileName string) string {
	if filepath.IsAbs(linkname) {
		return filepath.Clean(linkname)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(outFileName), linkname))
}

func extractEntry(
	tarReader *tar.Reader, header *tar.Header,
	outFileName string, options *Options,
) error {
	dirPerm := os.ModePerm
	if options.Perm != nil {
		dirPerm = *options.Perm
	}
	if err := os.MkdirAll(filepath.Dir(outFileName), dirPerm); err != nil {
		return err
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(outFileName, dirPerm)
	case tar.TypeSymlink:
		return os.Symlink(header.Linkname, outFileName)
	case tar.TypeLink:
		return os.Link(header.Linkname, outFileName)
	default:
		return extractRegularFile(tarReader, header, outFileName, options)
	}
}

func extractRegularFile(
	tarReader *tar.Reader,
	header *tar.Header,
	outFileName string,
	options *Options,
) error {
	filePerm := os.FileMode(0o644)
	if options.Perm != nil {
		filePerm = *options.Perm
	}
	outFile, err := openFileWithRetry(outFileName, filePerm)
	if err != nil {
		return err
	}
	defer func() { _ = outFile.Close() }()

	if _, err := io.Copy(outFile, tarReader); err != nil {
		return fmt.Errorf("io copy tar reader %s: %w", outFileName, err)
	}
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("out file close %s: %w", outFileName, err)
	}

	if options.Perm == nil {
		_ = os.Chmod(outFileName, header.FileInfo().Mode()|0o600)
	}
	_ = os.Chtimes(outFileName, time.Now(), header.FileInfo().ModTime())
	return nil
}

func openFileWithRetry(name string, perm os.FileMode) (*os.File, error) {
	flags := os.O_RDWR | os.O_CREATE | os.O_TRUNC
	f, err := os.OpenFile(filepath.Clean(name), flags, perm)
	if err != nil {
		time.Sleep(time.Second * 5)
		f, err = os.OpenFile(filepath.Clean(name), flags, perm)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", name, err)
		}
	}
	return f, nil
}

func getRelativeFromFullPath(fullpath string, prefix string) string {
	return strings.TrimPrefix(
		strings.ReplaceAll(strings.ReplaceAll(fullpath[len(prefix):], "\\", "/"), "//", "/"),
		".",
	)
}

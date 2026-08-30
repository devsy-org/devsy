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
	StripLevels       int
	Perm              *os.FileMode
	UID               *int
	GID               *int
	PreserveOwnership bool
}

type Option func(o *Options)

func StripLevels(levels int) Option {
	return func(o *Options) {
		o.StripLevels = levels
	}
}

// PreserveHeaderOwnership makes Extract apply each entry's tar-header uid/gid.
func PreserveHeaderOwnership() Option {
	return func(o *Options) {
		o.PreserveOwnership = true
	}
}

func Extract(origReader io.Reader, destFolder string, options ...Option) error {
	extractOptions := &Options{}
	for _, o := range options {
		o(extractOptions)
	}

	// read ahead
	bufioReader := bufio.NewReaderSize(origReader, 1024*1024)
	reader, closer, err := detectReader(bufioReader)
	if err != nil {
		return err
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
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

func detectReader(bufioReader *bufio.Reader) (io.Reader, io.Closer, error) {
	testBytes, err := bufioReader.Peek(2) // read 2 bytes
	if err != nil {
		return nil, nil, err
	}

	if testBytes[0] == 31 && testBytes[1] == 139 {
		gzipReader, err := gzip.NewReader(bufioReader)
		if err != nil {
			return nil, nil, fmt.Errorf("error decompressing: %w", err)
		}
		return gzipReader, gzipReader, nil
	}

	return bufioReader, nil, nil
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

// entryTarget groups the paths every entry-materializing step needs: the
// entry's own destination path, and destFolder for resolving hard-link
// targets against the archive root.
type entryTarget struct {
	outFileName string
	destFolder  string
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
	target := entryTarget{outFileName: filepath.Join(destFolder, rel), destFolder: destFolder}

	if !withinDir(target.outFileName, destFolder) {
		return false, fmt.Errorf(
			"path traversal detected: %s resolves outside destination",
			header.Name,
		)
	}

	switch header.Typeflag {
	case tar.TypeSymlink, tar.TypeLink:
		if err := validateLinkTarget(header, target, options); err != nil {
			return false, err
		}
	}

	if err := extractEntry(tarReader, header, target, options); err != nil {
		return false, err
	}
	return true, nil
}

// validateLinkTarget ensures a symlink or hard link target stays within destFolder.
func validateLinkTarget(header *tar.Header, target entryTarget, options *Options) error {
	linkTarget := resolveLinkTarget(header, target, options)
	if !withinDir(linkTarget, target.destFolder) {
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

// resolveLinkTarget resolves a link target to an absolute path. A symlink's
// Linkname is a filesystem path relative to the link's own directory (or
// absolute); a hard link's Linkname instead names another archive member,
// in the same root+StripLevels namespace as every entry's Name.
func resolveLinkTarget(header *tar.Header, target entryTarget, options *Options) string {
	if header.Typeflag == tar.TypeLink {
		rel := resolveRelativePath(&tar.Header{Name: header.Linkname}, options)
		// #nosec G305 -- rel is confined to destFolder by resolveRelativePath.
		return filepath.Clean(filepath.Join(target.destFolder, rel))
	}
	if filepath.IsAbs(header.Linkname) {
		return filepath.Clean(header.Linkname)
	}
	// #nosec G305 -- resolved path is validated against destFolder.
	return filepath.Clean(filepath.Join(filepath.Dir(target.outFileName), header.Linkname))
}

func extractEntry(
	tarReader *tar.Reader, header *tar.Header, target entryTarget, options *Options,
) error {
	if err := os.MkdirAll(filepath.Dir(target.outFileName), dirMode(options)); err != nil {
		return err
	}

	if err := createEntry(tarReader, header, target, options); err != nil {
		return err
	}

	return applyOwnership(target.outFileName, header, options)
}

// createEntry materializes one tar entry on disk according to its type.
func createEntry(
	tarReader *tar.Reader, header *tar.Header, target entryTarget, options *Options,
) error {
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target.outFileName, dirMode(options))
	case tar.TypeSymlink:
		return os.Symlink(header.Linkname, target.outFileName)
	case tar.TypeLink:
		return os.Link(resolveLinkTarget(header, target, options), target.outFileName)
	default:
		return extractRegularFile(tarReader, header, target.outFileName, options)
	}
}

// dirMode returns the directory permission mode to extract with.
func dirMode(options *Options) os.FileMode {
	if options.Perm != nil {
		return *options.Perm
	}
	return os.ModePerm
}

// applyOwnership chowns a freshly extracted entry when the options ask for
// it, preferring explicit UID/GID overrides over the entry's header values.
func applyOwnership(
	outFileName string, header *tar.Header, options *Options,
) error {
	uid, gid, ok := ownershipFor(header, options)
	if !ok {
		return nil
	}
	if err := os.Lchown(outFileName, uid, gid); err != nil {
		if os.Geteuid() != 0 && errors.Is(err, os.ErrPermission) {
			return nil
		}
		return fmt.Errorf("chown %s: %w", outFileName, err)
	}
	return nil
}

// ownershipFor resolves the uid/gid to apply and whether any chown is wanted.
func ownershipFor(header *tar.Header, options *Options) (int, int, bool) {
	if options.UID == nil && options.GID == nil {
		return header.Uid, header.Gid, options.PreserveOwnership
	}
	uid, gid := 0, 0
	if options.UID != nil {
		uid = *options.UID
	}
	if options.GID != nil {
		gid = *options.GID
	}
	return uid, gid, true
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

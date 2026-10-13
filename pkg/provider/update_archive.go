package provider

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/download"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/google/uuid"
)

// DownloadUpdateBinaries prepares an already isolated provider update tree.
// Unlike the ordinary downloader it confines archive materialization with os.Root.
// Caller validates/seeds the tree and separately handles absolute local references.
func DownloadUpdateBinaries(
	ctx context.Context,
	binaries map[string][]*ProviderBinary,
	target string,
) (map[string]string, error) {
	if err := validateUpdateBinaryKeys(binaries); err != nil {
		return nil, err
	}
	result := map[string]string{}
	for key, locations := range binaries {
		binary := updateBinaryForPlatform(locations)
		if binary == nil {
			return nil, fmt.Errorf(
				"no update binary %s matches %s/%s",
				key,
				runtime.GOOS,
				runtime.GOARCH,
			)
		}
		path, err := prepareUpdateBinary(ctx, key, binary, target)
		if err != nil {
			return nil, err
		}
		result[key] = path
	}
	// A reused alias can observe another requested family's later replacement.
	// Recheck the complete prepared result before any live publication.
	for key, locations := range binaries {
		binary := updateBinaryForPlatform(locations)
		if !updateBinaryMatches(result[key], binary.Checksum) {
			return nil, fmt.Errorf(
				"prepared update binary %s no longer matches its requested checksum",
				key,
			)
		}
	}
	return result, nil
}

func validateUpdateBinaryKeys(binaries map[string][]*ProviderBinary) error {
	for key := range binaries {
		if key == "" || optionNameRegEx.MatchString(key) {
			return fmt.Errorf("unsafe provider update binary key %q", key)
		}
	}
	return nil
}

func updateBinaryForPlatform(locations []*ProviderBinary) *ProviderBinary {
	for _, binary := range locations {
		if binary.OS == runtime.GOOS && binary.Arch == runtime.GOARCH {
			return binary
		}
	}
	return nil
}

func prepareUpdateBinary(
	ctx context.Context,
	key string,
	binary *ProviderBinary,
	target string,
) (string, error) {
	if filepath.IsAbs(binary.Path) {
		return "", fmt.Errorf(
			"update preparation must validate absolute reference separately: %s",
			binary.Path,
		)
	}
	family := filepath.Join(target, strings.ToLower(key))
	path := getBinaryPath(binary, family)
	// Installed aliases are reusable, but checksum misses must not delete their targets.
	if updateBinaryMatches(path, binary.Checksum) {
		return path, nil
	}
	archive := isRemotePath(binary.Path) && binary.ArchivePath != ""
	// A cache miss replaces the entire archive family. Do not first unlink a
	// selected old executable whose parent may intentionally be read-only.
	if archive && !cachedUpdateBinaryAvailable(binary) {
		return downloadWithRetryUsing(key, binary, func() (string, error) {
			return downloadConfinedUpdateArchive(ctx, binary, family)
		})
	}
	if err := prepareUpdateDestination(ctx, target, path); err != nil {
		return "", fmt.Errorf("isolate update destination for %s: %w", key, err)
	}
	if !archive {
		return downloadBinaryForPlatform(ctx, key, []*ProviderBinary{binary}, target)
	}
	return prepareUpdateArchive(ctx, key, binary, family)
}

func prepareUpdateArchive(
	ctx context.Context,
	key string,
	binary *ProviderBinary,
	family string,
) (string, error) {
	// The legacy cache contains only the executable; seeded sidecars remain unchanged on a hit.
	if fromCache(binary, family) {
		return getBinaryPath(binary, family), nil
	}
	return downloadWithRetryUsing(key, binary, func() (string, error) {
		return downloadConfinedUpdateArchive(ctx, binary, family)
	})
}

func cachedUpdateBinaryAvailable(binary *ProviderBinary) bool {
	path, err := getCachedBinaryPath(binary.Path)
	return err == nil && updateBinaryMatches(path, binary.Checksum)
}

// Match without the legacy verifier's destructive checksum-mismatch cleanup.
func updateBinaryMatches(path, checksum string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if checksum == "" {
		return true
	}
	actual, err := hash.File(path)
	return err == nil && strings.EqualFold(actual, checksum)
}

func downloadConfinedUpdateArchive(
	ctx context.Context,
	binary *ProviderBinary,
	family string,
) (_ string, err error) {
	if err := validUpdateArchiveName(binary.ArchivePath); err != nil {
		return "", err
	}
	if err := removeUpdateArchiveFamily(family); err != nil {
		return "", err
	}
	if err := os.MkdirAll(family, dirPerms); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(family)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err = fetchUpdateArchive(ctx, root, binary.Path); err != nil {
		return "", err
	}
	if err = finalizeUpdateArchive(ctx, root, binary.ArchivePath); err != nil {
		return "", err
	}
	return getBinaryPath(binary, family), nil
}

// Each download attempt replaces only its owned staged family; cache hits bypass this path.
func removeUpdateArchiveFamily(family string) error {
	info, err := os.Lstat(family)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := makeUpdateArchiveFamilyWritable(family); err != nil {
			return err
		}
	}
	return os.RemoveAll(family)
}

func makeUpdateArchiveFamilyWritable(family string) error {
	root, err := os.OpenRoot(family)
	if err != nil {
		return err
	}
	walkErr := fs.WalkDir(
		root.FS(),
		".",
		func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return root.Chmod(name, 0o700)
		},
	)
	return errors.Join(walkErr, root.Close())
}

func finalizeUpdateArchive(ctx context.Context, root *os.Root, selected string) error {
	if err := validateUpdateArchiveLinks(ctx, root); err != nil {
		return err
	}
	file, err := root.Open(selected)
	if err != nil {
		return err
	}
	return errors.Join(file.Chmod(filePerms), file.Close())
}

func fetchUpdateArchive(ctx context.Context, root *os.Root, url string) (err error) {
	body, err := download.File(ctx, url, download.WithCredentialResolver(gitcredentials.Resolver{}))
	if err != nil {
		return fmt.Errorf("download update archive: %w", err)
	}
	defer func() { err = errors.Join(err, body.Close()) }()
	switch {
	case strings.HasSuffix(url, zipSuffix):
		return extractConfinedUpdateZip(ctx, root, body)
	case isGzipOrTar(url):
		return extractConfinedUpdateTar(ctx, root, body)
	default:
		return fmt.Errorf("unrecognized update archive format %s", url)
	}
}

func validUpdateArchiveName(name string) error {
	clean := filepath.Clean(name)
	if name == "" || filepath.IsAbs(name) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe update archive member %q", name)
	}
	return nil
}

func updateArchiveParents(root *os.Root, name string) error {
	if err := validUpdateArchiveName(name); err != nil {
		return err
	}
	return root.MkdirAll(filepath.Dir(name), 0o755)
}

func openUpdateTarSource(input io.Reader) (io.ReadCloser, error) {
	buffered := bufio.NewReader(input)
	signature, err := buffered.Peek(2)
	if err != nil {
		return nil, err
	}
	if signature[0] == 31 && signature[1] == 139 {
		return gzip.NewReader(buffered)
	}
	return io.NopCloser(buffered), nil
}

func extractConfinedUpdateTar(ctx context.Context, root *os.Root, input io.Reader) (err error) {
	source, err := openUpdateTarSource(input)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	archive := tar.NewReader(source)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = materializeUpdateTarEntry(root, archive, header); err != nil {
			return fmt.Errorf("confined update archive member %q: %w", header.Name, err)
		}
	}
}

func materializeUpdateTarEntry(root *os.Root, input io.Reader, header *tar.Header) error {
	if header.Typeflag == tar.TypeXGlobalHeader {
		return nil
	}
	name := filepath.Clean(header.Name)
	if err := updateArchiveParents(root, header.Name); err != nil {
		return err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(name, 0o755)
	case tar.TypeSymlink:
		return materializeUpdateTarSymlink(root, name, header.Linkname)
	case tar.TypeLink:
		return materializeUpdateTarLink(root, name, header.Linkname)
	case tar.TypeReg:
		return writeConfinedUpdateFile(root, name, input, header.FileInfo().Mode().Perm()|0o600)
	default:
		return fmt.Errorf("unsupported update archive entry type %d", header.Typeflag)
	}
}

func materializeUpdateTarSymlink(root *os.Root, name, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("absolute archive symlink is unsupported: %s", target)
	}
	return root.Symlink(target, name)
}

func materializeUpdateTarLink(root *os.Root, name, target string) error {
	if err := validUpdateArchiveName(target); err != nil {
		return err
	}
	return root.Link(filepath.Clean(target), name)
}

func writeConfinedUpdateFile(root *os.Root, name string, input io.Reader, mode fs.FileMode) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, input)
	return errors.Join(copyErr, file.Chmod(mode.Perm()), file.Close())
}

func extractConfinedUpdateZip(ctx context.Context, root *os.Root, input io.Reader) (err error) {
	spoolName := ".update-zip-" + uuid.NewString()
	spool, err := root.OpenFile(spoolName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, spool.Close(), root.Remove(spoolName)) }()
	size, err := io.Copy(spool, input)
	if err != nil {
		return err
	}
	archive, err := zip.NewReader(spool, size)
	if err != nil {
		return err
	}
	for _, entry := range archive.File {
		if err = ctx.Err(); err != nil {
			return err
		}
		if filepath.Clean(entry.Name) == spoolName {
			return errors.New("archive member conflicts with owned spool")
		}
		if err = materializeUpdateZipEntry(root, entry); err != nil {
			return fmt.Errorf("confined update zip member %q: %w", entry.Name, err)
		}
	}
	return nil
}

func materializeUpdateZipEntry(root *os.Root, entry *zip.File) (err error) {
	if err := updateArchiveParents(root, entry.Name); err != nil {
		return err
	}
	name := filepath.Clean(entry.Name)
	if entry.FileInfo().IsDir() {
		return root.MkdirAll(name, 0o755)
	}
	body, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, body.Close()) }()
	if entry.Mode()&os.ModeSymlink != 0 {
		return materializeUpdateZipLink(root, name, body)
	}
	if !entry.Mode().IsRegular() {
		return fmt.Errorf("unsupported update zip file type %s", entry.Mode())
	}
	const limit = 2 << 30
	limited := &io.LimitedReader{R: body, N: limit + 1}
	err = writeConfinedUpdateFile(root, name, limited, entry.Mode())
	if limited.N == 0 {
		err = errors.Join(err, fmt.Errorf("update zip entry exceeds %d bytes", limit))
	}
	return err
}

func materializeUpdateZipLink(root *os.Root, name string, body io.Reader) error {
	target, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil {
		return err
	}
	if len(target) > 4096 {
		return errors.New("archive symlink target exceeds 4096 bytes")
	}
	if filepath.IsAbs(string(target)) {
		return errors.New("absolute archive symlink is unsupported")
	}
	return root.Symlink(string(target), name)
}

func validateUpdateArchiveLinks(ctx context.Context, root *os.Root) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := StatUpdatePath(root, name)
			if err != nil {
				return fmt.Errorf("unsafe update archive symlink %s: %w", name, err)
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported archive symlink target %s", name)
			}
		}
		return nil
	})
}

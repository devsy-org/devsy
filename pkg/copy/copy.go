package copy

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func Chown(path string, userName string) error {
	if userName == "" {
		return nil
	}

	uid := parseUserSpec(userName)
	userID, err := lookupUser(uid)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}

	uidInt, _ := strconv.Atoi(userID.Uid)
	gidInt, _ := strconv.Atoi(userID.Gid)
	return os.Lchown(path, uidInt, gidInt)
}

// ChownFailure is one entry a recursive chown could not reassign.
type ChownFailure struct {
	Path string
	Err  error
}

func (f ChownFailure) Error() string { return fmt.Sprintf("%s: %v", f.Path, f.Err) }

func (f ChownFailure) Unwrap() error { return f.Err }

// ChownFailures aggregates the entries ChownR could not chown.
type ChownFailures []ChownFailure

func (fs ChownFailures) Error() string {
	return fmt.Sprintf("%d entries could not be chowned, first: %v", len(fs), fs[0])
}

func (fs ChownFailures) Unwrap() []error {
	errs := make([]error, len(fs))
	for i, f := range fs {
		errs[i] = f
	}
	return errs
}

// AllDenied reports whether every failure was refused by the filesystem
// (permission denied or read-only share); the expected case for entries on
// virtiofs shares such as read-only .git pack files.
func (fs ChownFailures) AllDenied() bool {
	for _, f := range fs {
		if !DeniedByFilesystem(f.Err) {
			return false
		}
	}
	return len(fs) > 0
}

func ChownR(path string, userName string) error {
	if userName == "" {
		return nil
	}

	uid := parseUserSpec(userName)
	userID, err := lookupUser(uid)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}

	uidInt, _ := strconv.Atoi(userID.Uid)
	gidInt, _ := strconv.Atoi(userID.Gid)
	// #nosec G115 -- a resolved system uid is non-negative and fits uint32.
	uidU32 := uint32(uidInt)

	var failures ChownFailures
	_ = filepath.WalkDir(path, func(name string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			failures = append(failures, ChownFailure{Path: name, Err: err})
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			failures = append(failures, ChownFailure{Path: name, Err: err})
			return nil
		}
		if IsUID(info, uidU32) {
			return nil
		}
		// #nosec G122 -- best-effort chown of a freshly provisioned tree we own; WalkDir yields real paths.
		if lerr := os.Lchown(name, uidInt, gidInt); lerr != nil {
			failures = append(failures, ChownFailure{Path: name, Err: lerr})
		}
		return nil
	})
	if len(failures) == 0 {
		return nil
	}
	return failures
}

func MkdirAllChown(path string, perm os.FileMode, userName string) error {
	var created []string
	for cur := filepath.Clean(path); !Exists(cur); {
		created = append(created, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}

	for _, dir := range created {
		if err := Chown(dir, userName); err != nil {
			return err
		}
	}
	return nil
}

func RenameDirectory(srcDir, dest string) error {
	err := Directory(srcDir, dest)
	if err != nil {
		return err
	}

	return os.RemoveAll(srcDir)
}

func Directory(scrDir, dest string) error {
	if err := CreateIfNotExists(dest, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(scrDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyEntry(scrDir, dest, entry); err != nil {
			return err
		}
	}
	return nil
}

func copyEntry(scrDir, dest string, entry fs.DirEntry) error {
	sourcePath := filepath.Join(scrDir, entry.Name())
	destPath := filepath.Join(dest, entry.Name())

	fileInfo, err := entry.Info()
	if err != nil {
		return err
	}

	if err := copyByType(fileInfo, sourcePath, destPath); err != nil {
		return err
	}

	if err := Lchown(fileInfo, sourcePath, destPath); err != nil {
		return err
	}

	if fileInfo.Mode()&os.ModeSymlink == 0 {
		if err := os.Chmod(destPath, fileInfo.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyByType(fileInfo os.FileInfo, sourcePath, destPath string) error {
	switch fileInfo.Mode() & os.ModeType {
	case os.ModeDir:
		if err := CreateIfNotExists(destPath, 0o755); err != nil {
			return err
		}
		return Directory(sourcePath, destPath)
	case os.ModeSymlink:
		return Symlink(sourcePath, destPath)
	default:
		return File(sourcePath, destPath, 0o644)
	}
}

func File(srcFile, dstFile string, perm os.FileMode) error {
	out, err := os.OpenFile(dstFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	in, err := os.Open(srcFile)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return nil
}

func Exists(filePath string) bool {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false
	}

	return true
}

func CreateIfNotExists(dir string, perm os.FileMode) error {
	if Exists(dir) {
		return nil
	}

	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("failed to create directory: %q, error: %q", dir, err.Error())
	}

	return nil
}

func Symlink(source, dest string) error {
	link, err := os.Readlink(source)
	if err != nil {
		return err
	}
	return os.Symlink(link, dest)
}

func lookupUser(uid string) (*user.User, error) {
	userID, err := user.Lookup(uid)
	if err != nil {
		if _, parseErr := strconv.Atoi(uid); parseErr == nil {
			userID, err = user.LookupId(uid)
		}
	}
	return userID, err
}

func parseUserSpec(userSpec string) string {
	parts := strings.SplitN(userSpec, ":", 2)
	return parts[0]
}

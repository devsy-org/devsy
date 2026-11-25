package session

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// renameFile is Root.Rename, replaceable so a test can fail or pause a
// publish.
var renameFile = func(root *os.Root, oldName, newName string) error {
	return root.Rename(oldName, newName)
}

var (
	linkFile = (*os.Root).Link
	syncRoot = syncDirectory
)

// tempPattern matches the names stage gives its temp files.
var tempPattern = regexp.MustCompile(`^\.tmp-[0-9]+$`)

// entries lists the directory in name order.
func entries(root *os.Root) ([]fs.DirEntry, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open record directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read record directory: %w", err)
	}
	slices.SortFunc(
		entries,
		func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) },
	)
	return entries, nil
}

func isTemp(entry fs.DirEntry) bool {
	return tempPattern.MatchString(entry.Name()) && entry.Type().IsRegular()
}

func removeStrayTemps(root *os.Root) error {
	entries, err := entries(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !isTemp(entry) {
			continue
		}
		err := root.Remove(entry.Name())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stray temp file: %w", err)
		}
	}
	return nil
}

// publish links a new file into place without overwriting an existing entry.
func publish(root *os.Root, name string, data []byte) error {
	tmp, err := stage(root, data)
	if err != nil {
		return err
	}
	linkErr := linkFile(root, tmp, name)
	_ = root.Remove(tmp)
	if errors.Is(linkErr, fs.ErrExist) {
		return ErrExists
	}
	if linkErr != nil {
		return fmt.Errorf("publish record file: %w", linkErr)
	}
	return syncRoot(root)
}

func replace(root *os.Root, name string, data []byte) error {
	tmp, err := stage(root, data)
	if err != nil {
		return err
	}
	if err := renameFile(root, tmp, name); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("publish record file: %w", err)
	}
	return syncRoot(root)
}

// stage writes and syncs a temp file next to the final path.
func stage(root *os.Root, data []byte) (string, error) {
	f, name, err := createTemp(root)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(name)
		return "", fmt.Errorf("write record temp file: %w", err)
	}
	return name, nil
}

// createTemp makes a new file named like tempPattern and returns its name
// inside the directory. It never reuses a name.
func createTemp(root *os.Root) (*os.File, string, error) {
	for range tempAttempts {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, "", fmt.Errorf("name record temp file: %w", err)
		}
		name := tempPrefix + strconv.FormatUint(uint64(binary.BigEndian.Uint32(raw[:])), 10)
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create record temp file: %w", err)
		}
		return f, name, nil
	}
	return nil, "", fmt.Errorf("create record temp file: %w", fs.ErrExist)
}

// isRegularFile reports whether name is a regular file and the open file is
// that same file, not something a link led to.
func isRegularFile(root *os.Root, name string, opened fs.FileInfo) bool {
	link, err := root.Lstat(name)
	return err == nil && link.Mode().IsRegular() && opened.Mode().IsRegular() &&
		os.SameFile(link, opened)
}

// readRegular refuses a link, which Root follows inside the directory, and
// anything else that is not a regular file within the size cap.
func readRegular(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|nonBlock, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, missingFile(root, name)
	}
	if err != nil {
		return nil, fmt.Errorf("open record file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat record file: %w", err)
	}
	if !isRegularFile(root, name, info) {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read record file: %w", err)
	}
	if len(data) > maxFileBytes {
		return nil, errTooLarge
	}
	return data, nil
}

func missingFile(root *os.Root, name string) error {
	_, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat record file: %w", err)
	}
	return errNotRegular
}

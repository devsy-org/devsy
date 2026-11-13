package dependencies

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// metadataWriter only replaces mutable metadata. Callers in later transaction
// work must hold the relevant locks through read/check/write; this is not CAS.
// The caller owns root, an explicitly allocated private state directory.
type metadataWriter struct {
	root  *os.Root
	hooks metadataWriteHooks
}

type metadataDestination struct {
	root    *os.Root
	name    string
	journal bool
}

// Hooks are per writer/operation, never process-global filesystem replacements.
type metadataWriteHooks struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	rename  func(*os.Root, string, string) error
	syncDir func(*os.Root) error
}

// Published means replacement happened but durability was not established. A
// caller must inspect/reconcile under its locks, never assume rollback or retry.
type metadataCommitError struct {
	Published bool
	Err       error
}

func (e *metadataCommitError) Error() string {
	return fmt.Sprintf("dependency metadata replacement published=%t: %v", e.Published, e.Err)
}

func (e *metadataCommitError) Unwrap() error { return e.Err }

func newMetadataWriter(root *os.Root) (*metadataWriter, error) {
	if err := metadataWriteSupported(); err != nil {
		return nil, err
	}
	if err := validateMetadataRoot(root); err != nil {
		return nil, err
	}
	return &metadataWriter{root: root, hooks: metadataWriteHooks{
		write: (*os.File).Write, sync: (*os.File).Sync, close: (*os.File).Close,
		rename: (*os.Root).Rename, syncDir: syncMetadataDirectory,
	}}, nil
}

func validateMetadataRoot(root *os.Root) error {
	if root == nil {
		return errors.New("private metadata root is required")
	}
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("metadata root must be a directory")
	}
	return validatePrivateMetadata(info)
}

// initialize is the only directory-creating path. Opening and reading metadata
// never initialize state. The supplied root itself must already exist.
func (w *metadataWriter) initialize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.root.Mkdir("operations", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	operations, err := openMetadataOperations(w.root)
	if err != nil {
		return err
	}
	if err := operations.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.hooks.syncDir(w.root)
}

func openMetadataOperations(root *os.Root) (*os.Root, error) {
	before, err := root.Lstat("operations")
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, errors.New("metadata operations directory must not be a link")
	}
	child, err := root.OpenRoot("operations")
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err == nil && !os.SameFile(before, after) {
		err = errors.New("metadata operations directory changed")
	}
	if err == nil {
		err = validateMetadataRoot(child)
	}
	if err != nil {
		return nil, errors.Join(err, child.Close())
	}
	return child, nil
}

func (w *metadataWriter) writeRegistry(ctx context.Context, registry Registry) error {
	data, err := EncodeRegistry(registry)
	if err != nil {
		return err
	}
	return w.replace(ctx, metadataDestination{root: w.root, name: "registry.json"}, data)
}

func (w *metadataWriter) writeJournal(ctx context.Context, journal Journal) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := EncodeJournal(journal)
	if err != nil {
		return err
	}
	operations, err := openMetadataOperations(w.root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, operations.Close()) }()
	return w.replace(
		ctx,
		metadataDestination{root: operations, name: journal.OperationID + ".json", journal: true},
		data,
	)
}

type metadataTemporary struct {
	name     string
	file     *os.File
	identity os.FileInfo
}

func allocateMetadataTemp(
	ctx context.Context,
	destination metadataDestination,
) (*metadataTemporary, error) {
	if err := destination.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkExistingMetadata(destination); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := ".metadata-" + rand.Text() + ".tmp"
	file, err := destination.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	identity, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return &metadataTemporary{name: name, file: file, identity: identity}, nil
}

func (w *metadataWriter) replace(
	ctx context.Context,
	destination metadataDestination,
	data []byte,
) (err error) {
	temp, err := allocateMetadataTemp(ctx, destination)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		_ = temp.file.Close()
		if !published {
			err = errors.Join(err, removeMetadataTemp(destination.root, temp.name, temp.identity))
		}
	}()
	if err := w.prepare(ctx, temp.file, data); err != nil {
		return err
	}
	published, err = w.publish(ctx, destination, temp.name)
	return err
}

func (w *metadataWriter) publish(
	ctx context.Context,
	destination metadataDestination,
	tempName string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := w.hooks.rename(destination.root, tempName, destination.name); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return true, &metadataCommitError{Published: true, Err: err}
	}
	if err := w.hooks.syncDir(destination.root); err != nil {
		return true, &metadataCommitError{Published: true, Err: err}
	}
	return true, nil
}

func (w *metadataWriter) prepare(ctx context.Context, file *os.File, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := w.hooks.write(file, data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.hooks.sync(file); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.hooks.close(file)
}

func removeMetadataTemp(root *os.Root, name string, created os.FileInfo) error {
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(created, current) {
		return errors.New("metadata temporary file identity changed; left untouched")
	}
	return root.Remove(name)
}

func checkExistingMetadata(destination metadataDestination) error {
	data, err := readExistingMetadata(destination)
	if err != nil || data == nil {
		return err
	}
	// Replacement cannot silently repair corrupt or future documents. Recovery
	// requires an explicit policy under the later transaction locks.
	if !destination.journal {
		_, err := DecodeRegistry(bytes.NewReader(data))
		return err
	}
	existing, err := DecodeJournal(bytes.NewReader(data))
	if err == nil && existing.OperationID+".json" != destination.name {
		return errors.New("journal identity does not match metadata name")
	}
	return err
}

func readExistingMetadata(destination metadataDestination) (data []byte, err error) {
	file, err := openMetadataFile(destination.root, destination.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("metadata destination must be a regular file")
	}
	if err := validatePrivateMetadata(info); err != nil {
		return nil, err
	}
	data, err = io.ReadAll(io.LimitReader(file, maxMetadataBytes+1))
	if len(data) > maxMetadataBytes {
		return nil, errors.New("existing dependency metadata exceeds size limit")
	}
	return data, err
}

func (d metadataDestination) validate() error {
	if !d.journal {
		if d.name != "registry.json" {
			return errors.New("unrecognized registry metadata name")
		}
		return nil
	}
	if !strings.HasSuffix(d.name, ".json") || !metadataID(strings.TrimSuffix(d.name, ".json")) {
		return errors.New("invalid operation metadata name")
	}
	return nil
}

package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// prepareUpdateDestination isolates destination aliases and retains temporary
// write access only to staged parents it must mutate. Installed read-only reuse
// bypasses this helper. Callers must restore the original modes before publishing.
func prepareUpdateDestination(
	ctx context.Context,
	stageRoot, selectedPath string,
) (restore func() error, err error) {
	stageRoot, selected, err := absoluteUpdateDestination(stageRoot, selectedPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(stageRoot)
	if err != nil {
		return nil, err
	}
	permissions := updateDestinationPermissions{root: root, changed: map[string]fs.FileMode{}}
	if err := permissions.isolateDestination(ctx, selected); err != nil {
		return nil, errors.Join(err, permissions.restore())
	}
	return permissions.restore, nil
}

// prepareUpdateDestinationParent makes an archive family's staged parent writable
// while the family is removed and recreated, then restores its original mode.
func prepareUpdateDestinationParent(stageRoot, selectedPath string) (func() error, error) {
	stageRoot, selected, err := absoluteUpdateDestination(stageRoot, selectedPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(stageRoot)
	if err != nil {
		return nil, err
	}
	permissions := updateDestinationPermissions{root: root, changed: map[string]fs.FileMode{}}
	if err := permissions.allowWrite(filepath.Dir(selected)); err != nil {
		return nil, errors.Join(err, permissions.restore())
	}
	return permissions.restore, nil
}

func absoluteUpdateDestination(stageRoot, selectedPath string) (string, string, error) {
	stageRoot, err := filepath.Abs(stageRoot)
	if err != nil {
		return "", "", err
	}
	selectedPath, err = filepath.Abs(selectedPath)
	if err != nil {
		return "", "", err
	}
	selected, err := updateDestinationRelative(stageRoot, selectedPath)
	if err != nil {
		return "", "", err
	}
	if selected == "." {
		return "", "", errors.New("provider update destination cannot be the staging root")
	}
	// Keep canonical target comparisons consistent on hosts whose temporary
	// directory has a symlinked prefix, while retaining the lexical selection.
	canonical, err := filepath.EvalSymlinks(stageRoot)
	return canonical, selected, err
}

func (p *updateDestinationPermissions) isolateDestination(
	ctx context.Context,
	selected string,
) error {
	parts := strings.Split(selected, string(filepath.Separator))
	for index := range parts {
		name := filepath.Join(parts[:index+1]...)
		continued, err := p.isolateEntry(ctx, name, index == len(parts)-1)
		if err != nil {
			return err
		}
		if !continued {
			return nil
		}
	}
	return nil
}

func (p *updateDestinationPermissions) isolateEntry(
	ctx context.Context, name string, leaf bool,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := p.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, p.allowExistingParentWrite(filepath.Dir(name))
	}
	if err != nil {
		return false, err
	}
	if leaf {
		return false, p.isolateLeaf(name, info)
	}
	if err := p.isolateAncestor(ctx, name, info); err != nil {
		return false, err
	}
	return true, nil
}

func (p *updateDestinationPermissions) isolateLeaf(name string, info fs.FileInfo) error {
	if !info.IsDir() {
		if err := p.allowWrite(filepath.Dir(name)); err != nil {
			return err
		}
	}
	return removeUpdateDestinationLeaf(p.root, name, info)
}

func (p *updateDestinationPermissions) isolateAncestor(
	ctx context.Context, name string, info fs.FileInfo,
) error {
	if info.Mode()&os.ModeSymlink != 0 {
		if err := p.allowWrite(filepath.Dir(name)); err != nil {
			return err
		}
	}
	return isolateUpdateAncestor(ctx, p.root, name, info)
}

type updateDestinationPermissions struct {
	root    *os.Root
	changed map[string]fs.FileMode
	order   []string
}

func (p *updateDestinationPermissions) allowWrite(name string) error {
	if _, ok := p.changed[name]; ok {
		return nil
	}
	info, err := p.root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider update destination parent is not a directory: %s", name)
	}
	mode := info.Mode()
	if mode.Perm()&0o200 == 0 {
		if err := p.root.Chmod(name, mode|0o200); err != nil {
			return fmt.Errorf("make staged update parent writable %s: %w", name, err)
		}
		p.changed[name] = mode
		p.order = append(p.order, name)
	}
	return nil
}

func (p *updateDestinationPermissions) allowExistingParentWrite(name string) error {
	if _, err := p.root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return p.allowWrite(name)
}

func (p *updateDestinationPermissions) restore() error {
	var err error
	for _, name := range slices.Backward(p.order) {
		err = errors.Join(err, p.root.Chmod(name, p.changed[name]))
	}
	return errors.Join(err, p.root.Close())
}

func removeUpdateDestinationLeaf(root *os.Root, name string, info fs.FileInfo) error {
	if !info.IsDir() {
		return root.Remove(name)
	}
	return nil
}

func isolateUpdateAncestor(
	ctx context.Context,
	root *os.Root,
	name string,
	info fs.FileInfo,
) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return detachUpdateDirectory(ctx, root, root.Name(), name)
	}
	if !info.IsDir() {
		return fmt.Errorf("provider update destination ancestor is not a directory: %s", name)
	}
	return nil
}

func updateDestinationRelative(root, path string) (string, error) {
	name, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("provider update destination escapes staging: %s", path)
	}
	return name, nil
}

func resolveUpdateDestination(root *os.Root, stageRoot, name string) (string, error) {
	// Root.Stat checks confinement before EvalSymlinks supplies the canonical
	// target needed to preserve a copied link's meaning after relocation.
	if _, err := StatUpdatePath(root, name); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(stageRoot, name))
	if err != nil {
		return "", err
	}
	return updateDestinationRelative(stageRoot, resolved)
}

func detachUpdateDirectory(ctx context.Context, root *os.Root, stageRoot, name string) (err error) {
	source, err := resolveUpdateDirectory(root, stageRoot, name)
	if err != nil {
		return err
	}
	parent := filepath.Join(stageRoot, filepath.Dir(name))
	temporary, err := os.MkdirTemp(parent, ".update-destination-")
	if err != nil {
		return err
	}
	defer func() {
		if temporary != "" {
			err = errors.Join(err, removeUpdateDestinationClone(temporary))
		}
	}()
	clone := updateDirectoryClone{
		root: root, stage: stageRoot, source: source, destination: name, temporary: temporary,
	}
	if err := clone.copy(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	temporaryName := filepath.Join(filepath.Dir(name), filepath.Base(temporary))
	if err := root.Rename(temporaryName, name); err != nil {
		return err
	}
	temporary = ""
	return nil
}

func resolveUpdateDirectory(root *os.Root, stageRoot, name string) (string, error) {
	source, err := resolveUpdateDestination(root, stageRoot, name)
	if err != nil {
		return "", err
	}
	info, err := StatUpdatePath(root, source)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf(
			"provider update ancestor alias does not target a directory: %s",
			name,
		)
	}
	return source, nil
}

type updateDirectoryClone struct {
	root                                  *os.Root
	stage, source, destination, temporary string
}

type updateDirectoryMode struct {
	name string
	mode fs.FileMode
}

func (c *updateDirectoryClone) copy(ctx context.Context) (err error) {
	source, err := c.root.OpenRoot(c.source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	out, err := os.OpenRoot(c.temporary)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	directories, err := c.copyContents(ctx, source, out)
	if err != nil {
		return err
	}
	for _, directory := range slices.Backward(directories) {
		if err := out.Chmod(directory.name, directory.mode); err != nil {
			return err
		}
	}
	return nil
}

func (c *updateDirectoryClone) copyContents(
	ctx context.Context,
	source, out *os.Root,
) ([]updateDirectoryMode, error) {
	var directories []updateDirectoryMode
	err := fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// A link to an ancestor can place this temporary clone inside its source.
		if filepath.Join(c.stage, c.source, name) == c.temporary {
			return fs.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			directories = append(
				directories,
				updateDirectoryMode{name: name, mode: info.Mode().Perm()},
			)
			return out.MkdirAll(name, 0o700)
		}
		return c.copyEntry(source, out, name, info)
	})
	return directories, err
}

func (c *updateDirectoryClone) copyEntry(
	source, out *os.Root,
	name string,
	info fs.FileInfo,
) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return c.copyLink(name)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported provider update destination member: %s", name)
	}
	in, err := source.Open(name)
	if err != nil {
		return err
	}
	file, err := out.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.Join(err, in.Close())
	}
	_, copyErr := io.Copy(file, in)
	return errors.Join(copyErr, file.Chmod(info.Mode().Perm()), file.Close(), in.Close())
}

func (c *updateDirectoryClone) copyLink(name string) error {
	target, err := resolveUpdateDestination(c.root, c.stage, filepath.Join(c.source, name))
	if err != nil {
		return err
	}
	parent := filepath.Join(c.stage, c.destination, filepath.Dir(name))
	rebased, err := filepath.Rel(parent, filepath.Join(c.stage, target))
	if err != nil {
		return err
	}
	temporary, err := updateDestinationRelative(c.stage, c.temporary)
	if err != nil {
		return err
	}
	// Use the whole stage root: Windows determines directory-link type by
	// resolving its target, which can intentionally lie outside this clone.
	return c.root.Symlink(rebased, filepath.Join(temporary, name))
}

// Only an unpromoted owned clone is made writable for removal. Never traverse
// symlink directories or chmod their targets during cleanup.
func removeUpdateDestinationClone(path string) error {
	root, err := os.OpenRoot(path)
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
			if entry.Type()&os.ModeSymlink == 0 {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				return root.Chmod(name, info.Mode().Perm()|0o700)
			}
			return nil
		},
	)
	return errors.Join(walkErr, root.Close(), os.RemoveAll(path))
}

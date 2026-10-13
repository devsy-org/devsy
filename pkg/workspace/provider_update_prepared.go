package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

// The caller holds the provider operation lock. This protects preparation
// failures, not concurrent readers or a crash between separate file commits.
func replaceProviderPrepared(
	ctx context.Context,
	p ProviderParams,
	replacement *provider.ProviderConfig,
) (err error) {
	return replaceProviderPreparedWithOps(ctx, p, replacement, defaultProviderUpdateCommitOps())
}

func replaceProviderPreparedWithOps(
	ctx context.Context,
	p ProviderParams,
	replacement *provider.ProviderConfig,
	ops providerUpdateCommitOps,
) (err error) {
	prepared, err := prepareProviderReplacement(ctx, p, replacement)
	if err != nil {
		return err
	}
	defer func() {
		if prepared.retain {
			err = errors.Join(
				err,
				fmt.Errorf("provider recovery artifacts retained at %s", prepared.stage),
			)
		} else if cleanupErr := ops.removeStage(prepared.stage); cleanupErr != nil {
			if prepared.committed && err == nil {
				log.Warnf(
					"remove provider update staging %s after commit: %v",
					prepared.stage,
					cleanupErr,
				)
				return
			}
			err = errors.Join(
				err,
				fmt.Errorf("remove owned provider update staging: %w", cleanupErr),
			)
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	return prepared.commit(p, replacement, ops)
}

type preparedProviderReplacement struct {
	stage, live, manifest string
	oldManifest           []byte
	oldMode               fs.FileMode
	replaceBinaries       bool
	hadBinaries           bool
	backupMoved           bool
	installed             bool
	committed             bool
	retain                bool
}

func prepareProviderReplacement(
	ctx context.Context,
	p ProviderParams,
	replacement *provider.ProviderConfig,
) (_ *preparedProviderReplacement, err error) {
	prepared, err := newPreparedProviderReplacement(p, replacement)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeProviderUpdateStage(prepared.stage))
		}
	}()
	if !prepared.replaceBinaries {
		return prepared, ctx.Err()
	}
	if err = prepared.prepareBinaries(ctx, replacement.Binaries); err != nil {
		return nil, err
	}
	return prepared, nil
}

func newPreparedProviderReplacement(
	p ProviderParams,
	replacement *provider.ProviderConfig,
) (_ *preparedProviderReplacement, err error) {
	dir, err := provider.GetProviderDir(p.DevsyConfig.DefaultContext, replacement.Name)
	if err != nil {
		return nil, err
	}
	if err = requireRegularProviderPath(dir, true); err != nil {
		return nil, err
	}
	old, mode, err := readProviderUpdateManifest(dir)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(dir, ".update-staging-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeProviderUpdateStage(stage))
		}
	}()
	if err = saveProviderUpdateBackup(stage, old, mode); err != nil {
		return nil, err
	}
	return &preparedProviderReplacement{
		stage: stage, live: filepath.Join(dir, "binaries"),
		manifest: filepath.Join(dir, provider.ProviderConfigFile), oldManifest: old, oldMode: mode,
		replaceBinaries: len(replacement.Binaries) > 0,
	}, nil
}

func readProviderUpdateManifest(dir string) (data []byte, mode fs.FileMode, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err := root.Lstat(provider.ProviderConfigFile)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("provider update manifest must be a regular file")
	}
	data, err = root.ReadFile(provider.ProviderConfigFile)
	return data, info.Mode().Perm(), err
}

func saveProviderUpdateBackup(stage string, data []byte, mode fs.FileMode) (err error) {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.WriteFile("old-provider.json", data, mode)
}

func (p *preparedProviderReplacement) prepareBinaries(
	ctx context.Context,
	binaries map[string][]*provider.ProviderBinary,
) (err error) {
	target := filepath.Join(p.stage, "binaries")
	p.hadBinaries, err = copyProviderBinaryTree(ctx, p.live, target)
	if err != nil {
		return err
	}
	locations, err := stagedProviderLocations(ctx, binaries)
	if err != nil {
		return fmt.Errorf("download binaries: validate preparation: %w", err)
	}
	if _, err = provider.DownloadUpdateBinaries(ctx, locations, target); err != nil {
		return fmt.Errorf("download binaries: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Archive sidecars are promoted together, but no link may lead outside staging.
	_, err = copyProviderBinaryTree(ctx, target, "")
	return err
}

func requireRegularProviderPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe provider update symlink: %s", path)
	}
	if directory && info.IsDir() || !directory && info.Mode().IsRegular() {
		return nil
	}
	return fmt.Errorf("unsupported provider update file type: %s", path)
}

// A blank destination validates without copying. Seed regular bytes independently;
// contained relative links are copied as links only after their source graph validates.
func copyProviderBinaryTree(ctx context.Context, source, destination string) (_ bool, err error) {
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		if destination != "" {
			return false, os.MkdirAll(destination, 0o700)
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("unsafe provider update binaries root: %s", source)
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	if err = validateProviderSeed(ctx, root, source); err != nil {
		return false, err
	}
	if destination == "" {
		return true, nil
	}
	return true, copyProviderSeed(ctx, root, destination)
}

type providerSeedCopier struct {
	source, destination *os.Root
	links               map[string]string
	directoryModes      map[string]fs.FileMode
	directories         []string
}

func copyProviderSeed(ctx context.Context, source *os.Root, destination string) (err error) {
	if err = os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	out, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	copier := &providerSeedCopier{
		source: source, destination: out,
		links: map[string]string{}, directoryModes: map[string]fs.FileMode{},
	}
	err = fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return copier.copyEntry(name, entry)
	})
	if err != nil {
		return err
	}
	if err = copier.copyLinks(); err != nil {
		return err
	}
	if err = copier.applyDirectoryModes(); err != nil {
		return err
	}
	return validateProviderSeed(ctx, out, destination)
}

func (c *providerSeedCopier) applyDirectoryModes() error {
	// WalkDir records parents before children; apply final modes only after all writes.
	for _, name := range slices.Backward(c.directories) {
		if err := c.destination.Chmod(name, c.directoryModes[name]); err != nil {
			return err
		}
	}
	return nil
}

func (c *providerSeedCopier) copyEntry(name string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		c.links[name], err = c.source.Readlink(name)
		return err
	}
	if info.IsDir() {
		c.directoryModes[name] = info.Mode()
		c.directories = append(c.directories, name)
		return c.destination.MkdirAll(name, 0o700)
	}
	return copyProviderSeedFile(c.source, c.destination, name, info.Mode())
}

func copyProviderSeedFile(source, destination *os.Root, name string, mode fs.FileMode) error {
	in, err := source.Open(name)
	if err != nil {
		return err
	}
	target, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return errors.Join(err, in.Close())
	}
	_, copyErr := io.Copy(target, in)
	return errors.Join(copyErr, target.Chmod(mode.Perm()), target.Close(), in.Close())
}

func (c *providerSeedCopier) copyLinks() error {
	// Windows determines the link type from its target, so create dependencies first.
	for len(c.links) > 0 {
		copied := false
		for name, target := range c.links {
			// Keep dot components intact: a preceding symlink can change their resolution.
			_, err := provider.StatUpdatePath(
				c.destination,
				filepath.Dir(name)+string(filepath.Separator)+target,
			)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if err = c.destination.Symlink(target, name); err != nil {
				return err
			}
			delete(c.links, name)
			copied = true
		}
		if !copied {
			return errors.New("provider seed link dependencies could not be resolved")
		}
	}
	return nil
}

func validateProviderSeed(ctx context.Context, root *os.Root, display string) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateProviderSeedEntry(root, name, entry); err != nil {
			return fmt.Errorf("provider update seed %s: %w", display, err)
		}
		return nil
	})
}

func validateProviderSeedEntry(root *os.Root, name string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		resolved, err := provider.StatUpdatePath(root, name)
		if err != nil {
			return fmt.Errorf("unsafe provider update symlink %s: %w", name, err)
		}
		if !resolved.IsDir() && !resolved.Mode().IsRegular() {
			return fmt.Errorf("unsupported provider link target: %s", name)
		}
		return nil
	}
	if !entry.IsDir() && !entry.Type().IsRegular() {
		return fmt.Errorf("unsupported provider update file type: %s", name)
	}
	return nil
}

// Absolute files are user-owned references, not downloader targets. In particular,
// never call verifyOrRemoveBinary on them when a replacement checksum differs.
func stagedProviderLocations(
	ctx context.Context,
	binaries map[string][]*provider.ProviderBinary,
) (map[string][]*provider.ProviderBinary, error) {
	staged := make(map[string][]*provider.ProviderBinary, len(binaries))
	for key, locations := range binaries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		staged[key] = locations
		for _, location := range locations {
			if location.OS != runtime.GOOS || location.Arch != runtime.GOARCH {
				continue
			}
			if err := validateProviderUpdateLocation(location); err != nil {
				return nil, err
			}
			if filepath.IsAbs(location.Path) {
				delete(staged, key)
			}
			break
		}
	}
	return staged, nil
}

func validateProviderUpdateLocation(location *provider.ProviderBinary) error {
	if filepath.IsAbs(location.Path) {
		return validateAbsoluteProviderBinary(location)
	}
	for _, target := range []string{location.Name, location.ArchivePath} {
		clean := filepath.Clean(target)
		if filepath.IsAbs(target) || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe provider update binary target: %s", target)
		}
	}
	return nil
}

func validateAbsoluteProviderBinary(location *provider.ProviderBinary) error {
	info, err := os.Stat(location.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("absolute provider binary is not regular: %s", location.Path)
	}
	if location.Checksum == "" {
		return nil
	}
	sum, err := hash.File(location.Path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, location.Checksum) {
		return fmt.Errorf("absolute provider binary checksum mismatch: %s", location.Path)
	}
	return nil
}

// Per-call operations keep phase-failure tests deterministic without mutable globals.
type providerUpdateCommitOps struct {
	rename          func(string, string) error
	saveManifest    func(string, *provider.ProviderConfig) error
	updateConfig    func(string, string, func(*config.Config) error) error
	restoreManifest func(string, []byte, fs.FileMode) error
	removeStage     func(string) error
}

func defaultProviderUpdateCommitOps() providerUpdateCommitOps {
	return providerUpdateCommitOps{
		rename:          os.Rename,
		saveManifest:    provider.SaveProviderConfig,
		updateConfig:    config.UpdateConfig,
		restoreManifest: provider.WriteFileAtomic,
		removeStage:     removeProviderUpdateStage,
	}
}

func (p *preparedProviderReplacement) commit(
	params ProviderParams, replacement *provider.ProviderConfig, ops providerUpdateCommitOps,
) error {
	if p.replaceBinaries {
		if p.hadBinaries {
			if err := ops.rename(p.live, filepath.Join(p.stage, "backup")); err != nil {
				return fmt.Errorf("backup old provider binaries: %w", err)
			}
			p.backupMoved = true
		}
		if err := ops.rename(filepath.Join(p.stage, "binaries"), p.live); err != nil {
			return errors.Join(
				fmt.Errorf("promote provider binaries: %w", err),
				p.restoreBinaries(ops),
			)
		}
		p.installed = true
	}
	if err := ops.saveManifest(params.DevsyConfig.DefaultContext, replacement); err != nil {
		return p.recoverCommit(err, ops)
	}
	if err := ops.updateConfig(params.DevsyConfig.DefaultContext, "", func(c *config.Config) error {
		cleanupOldOptions(c, replacement)
		clearInitialized(c, replacement.Name)
		return nil
	}); err != nil {
		return p.recoverCommit(err, ops)
	}
	p.committed = true
	return nil
}

func (p *preparedProviderReplacement) recoverCommit(
	original error,
	ops providerUpdateCommitOps,
) error {
	// SaveProviderConfig can return a sync error after rename; restore even then.
	manifestErr := ops.restoreManifest(p.manifest, p.oldManifest, p.oldMode)
	binariesErr := p.restoreBinaries(ops)
	if manifestErr != nil || binariesErr != nil {
		p.retain = true
	}
	return errors.Join(original, manifestErr, binariesErr)
}

func (p *preparedProviderReplacement) restoreBinaries(ops providerUpdateCommitOps) error {
	if p.installed {
		// Retain failed new artifacts too; do not remove a live tree if this move fails.
		if err := ops.rename(p.live, filepath.Join(p.stage, "failed-new")); err != nil {
			p.retain = true
			return fmt.Errorf("retain failed provider binaries: %w", err)
		}
		p.installed = false
	}
	if p.backupMoved {
		if err := ops.rename(filepath.Join(p.stage, "backup"), p.live); err != nil {
			p.retain = true
			return fmt.Errorf("restore provider binary backup: %w", err)
		}
		p.backupMoved = false
	}
	return nil
}

// Only a disposable owned staging tree reaches this helper; retained recovery
// artifacts and the live provider tree must never be made writable for cleanup.
func removeProviderUpdateStage(stage string) error {
	root, err := os.OpenRoot(stage)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
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
			if !entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return root.Chmod(name, info.Mode()|0o700)
		},
	)
	closeErr := root.Close()
	return errors.Join(walkErr, closeErr, os.RemoveAll(stage))
}

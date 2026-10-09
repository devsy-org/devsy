package tunnelserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

type workspaceIgnorePolicy struct {
	sourcePath string
	present    bool
	patterns   []string
	matcher    *patternmatcher.PatternMatcher
}

func loadWorkspaceIgnore(root string) (workspaceIgnorePolicy, error) {
	policy := workspaceIgnorePolicy{sourcePath: filepath.Join(root, pkgconfig.IgnoreFileName)}
	// #nosec G304 -- root is an authorized transfer source.
	f, err := os.Open(policy.sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		// A dangling symlink is a present policy that cannot be read.
		if _, statErr := os.Lstat(policy.sourcePath); errors.Is(statErr, os.ErrNotExist) {
			return policy, nil
		}
	}
	if err != nil {
		return policy, ignoreUploadError(policy.sourcePath, err)
	}
	defer func() { _ = f.Close() }()
	policy.present = true
	policy.patterns, err = ignorefile.ReadAll(f)
	if err == nil {
		policy.matcher, err = patternmatcher.New(policy.patterns)
	}
	if err != nil {
		return policy, ignoreUploadError(policy.sourcePath, err)
	}
	return policy, nil
}

func ignoreUploadError(file string, err error) error {
	// Parser errors can contain the offending expression; keep those out of logs.
	log.Warnf("workspace upload stopped: cannot read or validate ignore file %q", file)
	return fmt.Errorf("workspace upload stopped: read or validate ignore file %q: %w", file, err)
}

func (t *tunnelServer) validateMountRoles() error {
	seen := map[string]bool{}
	mounts := make([]*config.Mount, 0, len(t.mounts))
	found := t.workspaceMount == nil
	for _, mount := range t.mounts {
		if mount == nil {
			return fmt.Errorf("invalid setup result: nil authorized mount")
		}
		identity := mount.String()
		if t.workspaceMount != nil && identity == t.workspaceMount.String() {
			found = true
		}
		// The RPC identifies mounts by serialization, so identical entries have one role.
		if !seen[identity] {
			mounts = append(mounts, mount)
			seen[identity] = true
		}
	}
	if !found {
		return fmt.Errorf("workspace mount is not in the authorized mount set")
	}
	t.mounts = mounts
	return nil
}

func (t *tunnelServer) mountTarOptions(
	ctx context.Context, mount *config.Mount, snapshot bool,
) (extract.TarOptions, func() error, error) {
	workspace := t.workspaceMount != nil && mount.String() == t.workspaceMount.String()
	return t.sourceTarOptions(ctx, mount.Source, workspace, snapshot)
}

func (t *tunnelServer) sourceTarOptions(
	ctx context.Context, root string, workspace, snapshot bool,
) (extract.TarOptions, func() error, error) {
	if !workspace {
		log.Debugf(
			"transfer role=additional-bind source=%s workspace ignore rules applied=false",
			root,
		)
		return extract.TarOptions{}, func() error { return nil }, nil
	}
	policy, err := loadWorkspaceIgnore(root)
	if err != nil {
		return extract.TarOptions{}, nil, err
	}
	log.Debugf("transfer role=workspace source=%s ignore=%s present=%t rules=%d applied=%t",
		root, policy.sourcePath, policy.present, len(policy.patterns), policy.present)
	generated, err := t.prepareGeneratedTransfer(ctx, root, snapshot)
	if err != nil {
		return extract.TarOptions{}, nil, fmt.Errorf(
			"workspace upload stopped: generated build artifacts: %w",
			err,
		)
	}
	opts := extract.TarOptions{
		Matcher:              policy.matcher,
		ProtectedFiles:       generated.files,
		ProtectedSymlinks:    generated.links,
		ProtectedDirectories: generated.directories,
	}
	if snapshot {
		opts, err = t.snapshotTarOptions(root, policy, generated.paths)
		if err != nil {
			return extract.TarOptions{}, nil, errors.Join(err, generated.close())
		}
	}
	return opts, generated.close, nil
}

func (t *tunnelServer) snapshotTarOptions(
	root string, policy workspaceIgnorePolicy, generated []string,
) (extract.TarOptions, error) {
	if t.snapshotBuildContextErr != nil {
		return extract.TarOptions{}, t.snapshotBuildContextErr
	}
	context := t.snapshotBuildContext
	if context == "" {
		context = t.generatedBuildContext
	}
	if context == "" {
		context = root
	}
	absoluteRoot, _, err := config.CanonicalWorkspacePath(root, root)
	if err != nil {
		return extract.TarOptions{}, err
	}
	_, absolute, err := config.CanonicalWorkspacePath(root, context)
	if err == nil {
		residue, relErr := relativeWithin(
			absoluteRoot,
			filepath.Join(absolute, config.DevsyContextFeatureFolder),
		)
		if relErr == nil {
			generated = append(append([]string(nil), generated...), residue)
		}
	}
	return snapshotTarOptions(policy, generated)
}

func snapshotTarOptions(
	policy workspaceIgnorePolicy,
	generated []string,
) (extract.TarOptions, error) {
	if len(generated) == 0 {
		return extract.TarOptions{Matcher: policy.matcher}, nil
	}
	patterns := append([]string(nil), policy.patterns...)
	for _, p := range generated {
		patterns = append(
			patterns,
			strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]", "!", "[!]").Replace(p),
		)
	}
	matcher, err := patternmatcher.New(patterns)
	return extract.TarOptions{Matcher: matcher}, err
}

func closeTransferPolicy(closePolicy func() error) {
	if err := closePolicy(); err != nil {
		log.Warnf("clean up generated artifact staging: %v", err)
	}
}

func relativeWithin(root, p string) (string, error) {
	relative, err := filepath.Rel(root, p)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q is outside transfer context %q", p, root)
	}
	return filepath.ToSlash(relative), nil
}

type generatedTransfer struct {
	files       map[string]extract.ProtectedFile
	links       map[string]string
	paths       []string
	directories []string
	staging     *os.File
	snapshot    bool
}

func (t *tunnelServer) prepareGeneratedTransfer(
	ctx context.Context, root string, snapshot bool,
) (*generatedTransfer, error) {
	generated := &generatedTransfer{
		files:    map[string]extract.ProtectedFile{},
		links:    map[string]string{},
		snapshot: snapshot,
	}
	if len(t.generatedBuildArtifacts) == 0 {
		return generated, nil
	}
	canonicalRoot, _, err := config.CanonicalWorkspacePath(root, root)
	if err != nil {
		return nil, err
	}
	contextRoot, err := t.artifactContextRoot(root)
	if err != nil {
		return nil, err
	}
	for _, artifact := range t.generatedBuildArtifacts {
		relative, err := validatedArtifactPath(root, contextRoot, artifact.Path)
		if err == nil {
			err = generated.add(ctx, canonicalRoot, relative, artifact)
		}
		if err != nil {
			return nil, errors.Join(err, generated.close())
		}
		if !artifact.Directory {
			generated.paths = append(generated.paths, relative)
		}
	}
	return generated, nil
}

func (g *generatedTransfer) close() error {
	if g.staging == nil {
		return nil
	}
	f := g.staging
	g.staging = nil
	return errors.Join(f.Close(), os.Remove(f.Name()))
}

func (g *generatedTransfer) add(
	ctx context.Context, root, relative string, artifact config.GeneratedBuildArtifact,
) error {
	if artifact.Directory {
		return g.addDirectory(root, relative)
	}
	if artifact.LinkTarget != "" {
		if err := approveGeneratedSymlink(root, relative, artifact.LinkTarget); err != nil {
			return err
		}
		g.links[relative] = artifact.LinkTarget
		return nil
	}
	if g.snapshot {
		_, err := copyApprovedArtifact(
			ctx,
			io.Discard,
			artifactSource{root, relative, artifact.SHA256},
		)
		return err
	}
	if err := g.ensureStaging(); err != nil {
		return err
	}
	offset, err := g.staging.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	size, err := copyApprovedArtifact(
		ctx,
		g.staging,
		artifactSource{root, relative, artifact.SHA256},
	)
	if err != nil {
		return err
	}
	g.files[relative] = extract.ProtectedFile{
		Reader: io.NewSectionReader(g.staging, offset, size),
		Size:   size,
	}
	return nil
}

func (g *generatedTransfer) addDirectory(root, relative string) error {
	info, err := artifactFileInfo(root, relative)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("generated directory %q is no longer a directory", relative)
	}
	g.directories = append(g.directories, relative)
	return nil
}

func (g *generatedTransfer) ensureStaging() error {
	if g.staging != nil {
		return nil
	}
	file, err := os.CreateTemp("", "devsy-build-artifacts-*")
	if err != nil {
		return fmt.Errorf("create generated artifact staging: %w", err)
	}
	g.staging = file
	return nil
}

func (t *tunnelServer) artifactContextRoot(root string) (string, error) {
	if t.generatedBuildContext == "" {
		return "", fmt.Errorf("missing generated build context")
	}
	_, contextRoot, err := config.CanonicalWorkspacePath(root, t.generatedBuildContext)
	return contextRoot, err
}

func validatedArtifactPath(root, contextRoot, artifact string) (string, error) {
	canonicalRoot, absolute, err := config.CanonicalWorkspacePath(root, artifact)
	if err != nil {
		return "", err
	}
	relative, err := relativeWithin(canonicalRoot, absolute)
	if err != nil {
		return "", err
	}
	for _, name := range config.BuildArtifactExcludes() {
		if _, err := relativeWithin(filepath.Join(contextRoot, name), absolute); err == nil {
			return relative, nil
		}
	}
	return "", fmt.Errorf("artifact %q is outside the generated build directory", artifact)
}

func artifactFileInfo(root, relative string) (os.FileInfo, error) {
	current := root
	var info os.FileInfo
	for component := range strings.SplitSeq(relative, "/") {
		current = filepath.Join(current, component)
		var err error
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 &&
			current != filepath.Join(root, filepath.FromSlash(relative)) {
			return nil, fmt.Errorf("generated artifact path %q contains a symlink", current)
		}
	}
	return info, nil
}

func approveGeneratedSymlink(root, relative, approved string) error {
	info, err := artifactFileInfo(root, relative)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("generated symlink %q changed since build preparation", relative)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	target, err := os.Readlink(absolute)
	if err != nil {
		return err
	}
	if target != approved {
		return fmt.Errorf("generated symlink %q changed since build preparation", relative)
	}
	if !generatedSymlinkTargetWithin(root, absolute, target) {
		return fmt.Errorf("generated symlink %q points outside the workspace", relative)
	}
	return nil
}

func generatedSymlinkTargetWithin(root, absolute, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	relative, err := filepath.Rel(root, filepath.Join(filepath.Dir(absolute), target))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

type artifactSource struct {
	root, relative, digest string
}

func copyApprovedArtifact(
	ctx context.Context,
	destination io.Writer,
	source artifactSource,
) (int64, error) {
	root, relative, digest := source.root, source.relative, source.digest
	info, err := artifactFileInfo(root, relative)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf(
			"generated artifact %q is not a regular file or is a symlink",
			relative,
		)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	// #nosec G304 -- validated build manifest path inside the authorized workspace.
	f, err := os.Open(absolute)
	if err != nil {
		return 0, err
	}
	h := sha256.New()
	reader := &contextArtifactReader{ctx: ctx, reader: io.LimitReader(f, info.Size()+1)}
	size, copyErr := io.Copy(io.MultiWriter(destination, h), reader)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return 0, err
	}
	if size != info.Size() || fmt.Sprintf("%x", h.Sum(nil)) != digest {
		return 0, fmt.Errorf("generated artifact %q changed since build preparation", absolute)
	}
	return size, nil
}

type contextArtifactReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextArtifactReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

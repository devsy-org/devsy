package tunnelserver

import (
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
	mount *config.Mount,
	snapshot bool,
) (extract.TarOptions, error) {
	workspace := t.workspaceMount != nil && mount.String() == t.workspaceMount.String()
	return t.sourceTarOptions(mount.Source, workspace, snapshot)
}

func (t *tunnelServer) sourceTarOptions(
	root string,
	workspace, snapshot bool,
) (extract.TarOptions, error) {
	if !workspace {
		log.Debugf(
			"transfer role=additional-bind source=%s workspace ignore rules applied=false",
			root,
		)
		return extract.TarOptions{}, nil
	}
	policy, err := loadWorkspaceIgnore(root)
	if err != nil {
		return extract.TarOptions{}, err
	}
	log.Debugf(
		"transfer role=workspace source=%s ignore=%s present=%t rules=%d applied=%t",
		root,
		policy.sourcePath,
		policy.present,
		len(policy.patterns),
		policy.present,
	)
	protected, err := t.protectedBuildFiles(root)
	if err != nil {
		return extract.TarOptions{}, fmt.Errorf(
			"workspace upload stopped: generated build artifacts: %w",
			err,
		)
	}
	opts := extract.TarOptions{Matcher: policy.matcher, ProtectedFiles: protected}
	if snapshot && len(protected) > 0 {
		patterns := append([]string(nil), policy.patterns...)
		for p := range protected {
			// These are literal paths, not user-authored expressions.
			patterns = append(
				patterns,
				strings.NewReplacer(`\`, `\\`, "[", `\[`, "*", `\*`, "?", `\?`).Replace(p),
			)
		}
		opts.Matcher, err = patternmatcher.New(patterns)
		opts.ProtectedFiles = nil
	}
	return opts, err
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

func (t *tunnelServer) protectedBuildFiles(root string) (map[string][]byte, error) {
	if len(t.generatedBuildArtifacts) == 0 {
		return nil, nil
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	contextRoot, err := t.artifactContextRoot(root)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(t.generatedBuildArtifacts))
	for _, artifact := range t.generatedBuildArtifacts {
		relative, err := validatedArtifactPath(root, contextRoot, artifact.Path)
		if err != nil {
			return nil, err
		}
		contents, err := readApprovedArtifact(root, relative, artifact.SHA256)
		if err != nil {
			return nil, err
		}
		files[relative] = contents
	}
	return files, nil
}

func (t *tunnelServer) artifactContextRoot(root string) (string, error) {
	contextRoot, err := filepath.Abs(t.generatedBuildContext)
	if err != nil || t.generatedBuildContext == "" {
		return "", fmt.Errorf("missing generated build context")
	}
	if contextRoot != root {
		if _, err := relativeWithin(root, contextRoot); err != nil {
			return "", err
		}
	}
	return contextRoot, nil
}

func validatedArtifactPath(root, contextRoot, artifact string) (string, error) {
	absolute, err := filepath.Abs(artifact)
	if err != nil {
		return "", err
	}
	relative, err := relativeWithin(root, absolute)
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
	components := append([]string{""}, strings.Split(relative, "/")...)
	for _, component := range components {
		current = filepath.Join(current, component)
		var err error
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("generated artifact path %q contains a symlink", current)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("generated artifact %q is not a regular file", current)
	}
	return info, nil
}

func readApprovedArtifact(root, relative, digest string) ([]byte, error) {
	info, err := artifactFileInfo(root, relative)
	if err != nil {
		return nil, err
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	// #nosec G304 -- validated build manifest path inside the authorized workspace.
	f, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	contents, err := io.ReadAll(io.LimitReader(f, info.Size()+1))
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(contents)) != info.Size() || fmt.Sprintf("%x", sha256.Sum256(contents)) != digest {
		return nil, fmt.Errorf("generated artifact %q changed since build preparation", absolute)
	}
	return contents, nil
}

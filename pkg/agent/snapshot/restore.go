// Package snapshot restores a workspace's mount contents on the agent side
// from a container snapshot, pulling the volumes blob directly from the
// registry (see pkg/snapshot) rather than via a new RPC.
package snapshot

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	pkgsnapshot "github.com/devsy-org/devsy/pkg/snapshot"
)

// RestoreVolumes pulls the volumes blob referenced by the snapshot at
// snapshotRef and extracts it into the single mount's target.
//
// The combined volumes archive is built by tunnelserver.appendDirToTar with
// every entry prefixed by the CREATE-time mount's target path (leading "/"
// trimmed, recorded as manifest.Annotations[AnnotationMountPrefix]). Strip
// depth is derived from that recorded prefix, not from the restore-side
// mount target's own path: the two are not guaranteed to have the same
// number of path segments (different provider defaults, different
// workspace-folder conventions), and deriving it from the wrong side would
// strip the wrong number of components and corrupt extracted paths.
//
// Only a single mount is supported today: the combined tar has no way here
// to disambiguate which entries belong to which mount when there is more
// than one, so restoring mounts[0] alone would silently drop the rest.
// Multi-mount restore is tracked as follow-up work; snapshot create already
// rejects multi-mount workspaces up front so this case should not arise
// from a snapshot this codebase created.
//
// extract.Extract only ever writes/overwrites entries present in the tar; it
// never deletes anything already at the target that the tar doesn't mention.
// So when reset is true, the target's existing contents are removed first,
// giving --reset its expected meaning: the workspace ends up in exactly the
// snapshot's state, not the snapshot's state merged with whatever was there.
func RestoreVolumes(
	ctx context.Context, snapshotRef string, mounts []*config.Mount, reset bool,
) error {
	if len(mounts) == 0 {
		return fmt.Errorf("restore volumes: no mounts configured")
	}
	if len(mounts) > 1 {
		return fmt.Errorf(
			"snapshot restore does not yet support multiple mounts (%d found)",
			len(mounts),
		)
	}

	layer, err := resolveVolumesLayer(ctx, snapshotRef)
	if err != nil {
		return err
	}

	rc, err := pkgsnapshot.PullBlob(ctx, layer.Repository, layer.Digest)
	if err != nil {
		return fmt.Errorf("pull volumes blob: %w", err)
	}
	defer func() { _ = rc.Close() }()

	target := mounts[0].Target
	if reset {
		if err := clearDir(target); err != nil {
			return fmt.Errorf("clear %s for --reset: %w", target, err)
		}
	}

	levels := len(strings.Split(layer.MountPrefix, "/"))
	if err := extract.Extract(rc, target, extract.StripLevels(levels)); err != nil {
		return fmt.Errorf("extract snapshot volumes into %s: %w", target, err)
	}
	return nil
}

// clearDir removes dir's contents (not dir itself, which may be a bind mount
// that can't be unlinked and recreated).
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(dir + "/" + entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// volumesLayer locates a snapshot's volumes blob and how to extract it.
type volumesLayer struct {
	Digest      string
	MountPrefix string
	Repository  string
}

// resolveVolumesLayer pulls the manifest for snapshotRef and returns the
// volumes layer's digest, the create-time mount prefix needed to strip the
// right number of path segments on extract, and the repository to pull the
// blob from.
func resolveVolumesLayer(ctx context.Context, snapshotRef string) (*volumesLayer, error) {
	tag, err := pkgsnapshot.ParseRef(snapshotRef)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot ref %s: %w", snapshotRef, err)
	}

	manifest, err := pkgsnapshot.PullManifest(ctx, snapshotRef)
	if err != nil {
		return nil, fmt.Errorf("pull snapshot manifest %s: %w", snapshotRef, err)
	}

	volumes, err := manifest.Volumes()
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", snapshotRef, err)
	}
	digest := volumes.Digest

	mountPrefix, ok := manifest.Annotations[pkgsnapshot.AnnotationMountPrefix]
	if !ok || mountPrefix == "" {
		return nil, fmt.Errorf(
			"snapshot %s is missing its %s annotation; cannot determine how many "+
				"path segments to strip on extract",
			snapshotRef, pkgsnapshot.AnnotationMountPrefix,
		)
	}

	return &volumesLayer{Digest: digest, MountPrefix: mountPrefix, Repository: tag.Repository}, nil
}

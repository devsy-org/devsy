// Package snapshot restores a workspace's mount contents on the agent side
// from a container snapshot, pulling the volumes blob directly from the
// registry (see pkg/snapshot) rather than via a new RPC.
package snapshot

import (
	"context"
	"fmt"
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
func RestoreVolumes(ctx context.Context, snapshotRef string, mounts []*config.Mount) error {
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
	levels := len(strings.Split(layer.MountPrefix, "/"))
	if err := extract.Extract(rc, target, extract.StripLevels(levels)); err != nil {
		return fmt.Errorf("extract snapshot volumes into %s: %w", target, err)
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

	var digest string
	for _, l := range manifest.Layers {
		if l.MediaType == pkgsnapshot.VolumesMediaType {
			digest = l.Digest
			break
		}
	}
	if digest == "" {
		return nil, fmt.Errorf("snapshot %s has no volumes layer", snapshotRef)
	}

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

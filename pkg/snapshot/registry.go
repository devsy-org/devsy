package snapshot

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/devsy-org/devsy/pkg/image"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// rawTaggable lets us push arbitrary manifest bytes (the ORAS-style pattern):
// remote.Put only needs RawManifest and MediaType, it never interprets the
// content the way remote.Write does for a full v1.Image.
type rawTaggable struct {
	raw       []byte
	mediaType types.MediaType
}

func (r rawTaggable) RawManifest() ([]byte, error)        { return r.raw, nil }
func (r rawTaggable) MediaType() (types.MediaType, error) { return r.mediaType, nil }

func remoteOptions(ctx context.Context) ([]remote.Option, error) {
	keychain, err := image.GetKeychain(ctx)
	if err != nil {
		return nil, fmt.Errorf("create authentication keychain: %w", err)
	}
	return []remote.Option{remote.WithContext(ctx), remote.WithAuthFromKeychain(keychain)}, nil
}

// PushBlob pushes the content of r as a content-addressed blob of mediaType
// into repository and returns its digest and size. The blob is stored
// verbatim (no gzip envelope), so the returned digest and size describe the
// exact bytes read from r, and PullBlob returns them unchanged.
func PushBlob(
	ctx context.Context, repository, mediaType string, r io.Reader,
) (string, int64, error) {
	repo, err := parseRepository(repository)
	if err != nil {
		return "", 0, fmt.Errorf("parse repository %q: %w", repository, err)
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, fmt.Errorf("read blob content: %w", err)
	}

	opts, err := remoteOptions(ctx)
	if err != nil {
		return "", 0, err
	}

	layer := static.NewLayer(b, types.MediaType(mediaType))
	if err := remote.WriteLayer(repo, layer, opts...); err != nil {
		return "", 0, fmt.Errorf(
			"push blob to %s: %w",
			repository,
			image.SanitizeRegistryError(err),
		)
	}

	digest, err := layer.Digest()
	if err != nil {
		return "", 0, fmt.Errorf("read pushed blob digest: %w", err)
	}
	size, err := layer.Size()
	if err != nil {
		return "", 0, fmt.Errorf("read pushed blob size: %w", err)
	}
	return digest.String(), size, nil
}

// PushBlobStreaming pushes the content of r as a content-addressed blob of
// mediaType into repository, like PushBlob, but spools r to a temp file
// instead of buffering it in memory. Use this for blobs that may be large
// (e.g. workspace volume tars); PushBlob remains the simpler choice for
// small, already in-memory content such as manifests.
func PushBlobStreaming(
	ctx context.Context, repository, mediaType string, r io.Reader,
) (string, int64, error) {
	repo, err := parseRepository(repository)
	if err != nil {
		return "", 0, fmt.Errorf("parse repository %q: %w", repository, err)
	}

	f, err := os.CreateTemp("", "devsy-snapshot-blob-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file for blob spool: %w", err)
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()

	size, err := io.Copy(f, r)
	closeErr := f.Close()
	if err != nil {
		return "", 0, fmt.Errorf("spool blob to temp file: %w", err)
	}
	if closeErr != nil {
		return "", 0, fmt.Errorf("close spooled blob temp file: %w", closeErr)
	}

	digest, err := digestOfFile(path)
	if err != nil {
		return "", 0, err
	}

	opts, err := remoteOptions(ctx)
	if err != nil {
		return "", 0, err
	}

	layer := &fileLayer{
		path:      path,
		mediaType: types.MediaType(mediaType),
		digest:    digest,
		size:      size,
	}
	if err := remote.WriteLayer(repo, layer, opts...); err != nil {
		return "", 0, fmt.Errorf(
			"push blob to %s: %w",
			repository,
			image.SanitizeRegistryError(err),
		)
	}
	return digest.String(), size, nil
}

func digestOfFile(path string) (v1.Hash, error) {
	f, err := os.Open(path) //nolint:gosec // path is our own os.CreateTemp output, not user input
	if err != nil {
		return v1.Hash{}, fmt.Errorf("open spooled blob for digest: %w", err)
	}
	defer func() { _ = f.Close() }()

	digest, _, err := v1.SHA256(f)
	if err != nil {
		return v1.Hash{}, fmt.Errorf("compute spooled blob digest: %w", err)
	}
	return digest, nil
}

// fileLayer is a v1.Layer backed by a file on disk holding the blob's exact
// bytes verbatim (no gzip envelope), mirroring static.NewLayer's semantics
// (Digest == DiffID, Compressed == Uncompressed) but without requiring the
// content to be resident in memory.
type fileLayer struct {
	path      string
	mediaType types.MediaType
	digest    v1.Hash
	size      int64
}

func (l *fileLayer) Digest() (v1.Hash, error) { return l.digest, nil }
func (l *fileLayer) DiffID() (v1.Hash, error) { return l.digest, nil }

func (l *fileLayer) Compressed() (io.ReadCloser, error) {
	return os.Open(l.path)
}

func (l *fileLayer) Uncompressed() (io.ReadCloser, error) {
	return os.Open(l.path)
}

func (l *fileLayer) Size() (int64, error) { return l.size, nil }

func (l *fileLayer) MediaType() (types.MediaType, error) { return l.mediaType, nil }

func PullBlob(ctx context.Context, repository, digest string) (io.ReadCloser, error) {
	repo, err := parseRepository(repository)
	if err != nil {
		return nil, fmt.Errorf("parse repository %q: %w", repository, err)
	}
	dig := repo.Digest(digest)

	opts, err := remoteOptions(ctx)
	if err != nil {
		return nil, err
	}

	layer, err := remote.Layer(dig, opts...)
	if err != nil {
		return nil, fmt.Errorf("pull blob %s: %w", digest, image.SanitizeRegistryError(err))
	}
	rc, err := layer.Compressed()
	if err != nil {
		return nil, fmt.Errorf("open blob %s: %w", digest, err)
	}
	return rc, nil
}

// pushEmptyConfig ensures the manifest's required empty config blob exists.
func pushEmptyConfig(ctx context.Context, repository string) error {
	repo, err := parseRepository(repository)
	if err != nil {
		return fmt.Errorf("parse repository %q: %w", repository, err)
	}
	opts, err := remoteOptions(ctx)
	if err != nil {
		return err
	}
	layer := static.NewLayer(emptyConfigBytes, types.MediaType(emptyConfigMediaType))
	if err := remote.WriteLayer(repo, layer, opts...); err != nil {
		return fmt.Errorf(
			"push empty config to %s: %w",
			repository,
			image.SanitizeRegistryError(err),
		)
	}
	return nil
}

// The manifest's config/layer digests must already exist in the repository
// (pushed via PushBlob).
func PushManifest(ctx context.Context, ref string, m *Manifest) error {
	tag, err := parseTag(ref)
	if err != nil {
		return fmt.Errorf("parse snapshot ref %q: %w", ref, err)
	}

	if err := pushEmptyConfig(ctx, tag.Context().Name()); err != nil {
		return err
	}

	raw, err := m.MarshalOCI()
	if err != nil {
		return err
	}

	opts, err := remoteOptions(ctx)
	if err != nil {
		return err
	}

	if err := remote.Put(
		tag,
		rawTaggable{raw: raw, mediaType: types.MediaType(m.MediaType)},
		opts...); err != nil {
		return fmt.Errorf("push snapshot manifest %s: %w", ref, image.SanitizeRegistryError(err))
	}
	return nil
}

func PullManifest(ctx context.Context, ref string) (*Manifest, error) {
	tag, err := parseTag(ref)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot ref %q: %w", ref, err)
	}

	opts, err := remoteOptions(ctx)
	if err != nil {
		return nil, err
	}

	desc, err := remote.Get(tag, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"pull snapshot manifest %s: %w",
			ref,
			image.SanitizeRegistryError(err),
		)
	}
	return ParseManifest(desc.Manifest)
}

// DeleteManifest deletes the manifest tag at ref. Orphaned blobs are left for
// the registry's own garbage collection, matching the design's atomicity
// model (manifest visibility is the only thing that matters).
//
// Real registries (e.g. registry:2) reject DELETE-by-tag with 400
// DIGEST_INVALID and only accept DELETE-by-digest, so this resolves ref to
// its digest via HEAD before deleting, rather than deleting the tag
// reference directly.
func DeleteManifest(ctx context.Context, ref string) error {
	tag, err := parseTag(ref)
	if err != nil {
		return fmt.Errorf("parse snapshot ref %q: %w", ref, err)
	}
	opts, err := remoteOptions(ctx)
	if err != nil {
		return err
	}

	desc, err := remote.Head(tag, opts...)
	if err != nil {
		return fmt.Errorf("delete snapshot manifest %s: %w", ref, image.SanitizeRegistryError(err))
	}

	digestRef := tag.Context().Digest(desc.Digest.String())
	if err := remote.Delete(digestRef, opts...); err != nil {
		return fmt.Errorf("delete snapshot manifest %s: %w", ref, image.SanitizeRegistryError(err))
	}
	return nil
}

// ListRefs lists snapshot tags in repository belonging to workspaceID,
// newest first. Tags are filtered by the "<workspace-id>-<timestamp>" naming
// convention rather than by pulling every manifest, since registries expose
// tag lists cheaply but annotations only after a manifest GET.
func ListRefs(ctx context.Context, repository, workspaceID string) ([]*Ref, error) {
	repo, err := parseRepository(repository)
	if err != nil {
		return nil, fmt.Errorf("parse repository %q: %w", repository, err)
	}
	opts, err := remoteOptions(ctx)
	if err != nil {
		return nil, err
	}

	tags, err := remote.List(repo, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"list snapshots in %s: %w",
			repository,
			image.SanitizeRegistryError(err),
		)
	}

	var refs []*Ref
	for _, tag := range tags {
		ref, err := ParseRef(repository + ":" + tag)
		if err != nil {
			continue // skip tags that aren't snapshot refs
		}
		if ref.WorkspaceID == workspaceID {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Timestamp.After(refs[j].Timestamp) })
	return refs, nil
}

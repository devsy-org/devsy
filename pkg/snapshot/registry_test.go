package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/require"
)

const testDockerImageMediaType = "application/vnd.docker.distribution.manifest.v2+json"

func newTestRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestPushPullBlob_RoundTrip(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()

	digest, size, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		strings.NewReader("hello volumes"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, digest)
	require.EqualValues(t, len("hello volumes"), size)

	rc, err := PullBlob(ctx, host+"/acme/snapshots", digest)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello volumes", string(got))
}

func TestPushBlobStreaming_RoundTrip(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()

	digest, size, err := PushBlobStreaming(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		strings.NewReader("hello streamed volumes"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, digest)
	require.EqualValues(t, len("hello streamed volumes"), size)

	rc, err := PullBlob(ctx, host+"/acme/snapshots", digest)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello streamed volumes", string(got))
}

func TestPushBlobStreaming_MatchesPushBlobDigest(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()
	content := "same bytes either way"

	streamedDigest, streamedSize, err := PushBlobStreaming(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		strings.NewReader(content),
	)
	require.NoError(t, err)

	bufferedDigest, bufferedSize, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		strings.NewReader(content),
	)
	require.NoError(t, err)

	require.Equal(t, bufferedDigest, streamedDigest)
	require.Equal(t, bufferedSize, streamedSize)
}

func TestPushPullManifest_RoundTrip(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()

	volDigest, volSize, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		bytes.NewReader([]byte("vol-data")),
	)
	require.NoError(t, err)

	imgDigest, imgSize, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		testDockerImageMediaType,
		bytes.NewReader([]byte("fake-image-blob")),
	)
	require.NoError(t, err)

	m, err := BuildManifest(BuildManifestOptions{
		WorkspaceUID:         "uid-1",
		CreatedAt:            time.Now(),
		SourceProvider:       testSourceProvider,
		ContainerImageDigest: imgDigest,
		ContainerImageSize:   imgSize,
		VolumesDigest:        volDigest,
		VolumesSize:          volSize,
	})
	require.NoError(t, err)

	ref, err := NewRef(host+"/acme/snapshots", "my-ws", time.Now())
	require.NoError(t, err)

	require.NoError(t, PushManifest(ctx, ref.String(), m))

	pulled, err := PullManifest(ctx, ref.String())
	require.NoError(t, err)
	require.Equal(t, "uid-1", pulled.Annotations[AnnotationWorkspaceUID])
	require.Equal(t, volDigest, pulled.Layers[1].Digest)
}

func TestListRefs_FiltersByWorkspaceID(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()
	repo := host + "/acme/snapshots"

	now := time.Now()
	for i, ws := range []string{"my-ws", "my-ws", "other-ws"} {
		volDigest, volSize, err := PushBlob(ctx, repo, VolumesMediaType, strings.NewReader("v"))
		require.NoError(t, err)
		imgDigest, imgSize, err := PushBlob(
			ctx, repo, testDockerImageMediaType, strings.NewReader("i"),
		)
		require.NoError(t, err)
		m, err := BuildManifest(BuildManifestOptions{
			WorkspaceUID:         ws,
			CreatedAt:            now.Add(time.Duration(i) * time.Second),
			SourceProvider:       testSourceProvider,
			ContainerImageDigest: imgDigest,
			ContainerImageSize:   imgSize,
			VolumesDigest:        volDigest,
			VolumesSize:          volSize,
		})
		require.NoError(t, err)
		ref, err := NewRef(repo, ws, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		require.NoError(t, PushManifest(ctx, ref.String(), m))
	}

	refs, err := ListRefs(ctx, repo, "my-ws")
	require.NoError(t, err)
	require.Len(t, refs, 2)
	for _, r := range refs {
		require.Equal(t, "my-ws", r.WorkspaceID)
	}
}

func TestDeleteManifest_RemovesManifest(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()

	volDigest, volSize, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		VolumesMediaType,
		bytes.NewReader([]byte("vol-data")),
	)
	require.NoError(t, err)

	imgDigest, imgSize, err := PushBlob(
		ctx,
		host+"/acme/snapshots",
		testDockerImageMediaType,
		bytes.NewReader([]byte("fake-image-blob")),
	)
	require.NoError(t, err)

	m, err := BuildManifest(BuildManifestOptions{
		WorkspaceUID:         "uid-1",
		CreatedAt:            time.Now(),
		SourceProvider:       testSourceProvider,
		ContainerImageDigest: imgDigest,
		ContainerImageSize:   imgSize,
		VolumesDigest:        volDigest,
		VolumesSize:          volSize,
	})
	require.NoError(t, err)

	ref, err := NewRef(host+"/acme/snapshots", "my-ws", time.Now())
	require.NoError(t, err)

	require.NoError(t, PushManifest(ctx, ref.String(), m))

	pulled, err := PullManifest(ctx, ref.String())
	require.NoError(t, err)
	require.NotNil(t, pulled)

	require.NoError(t, DeleteManifest(ctx, ref.String()))

	// DeleteManifest deletes by digest (real registries reject
	// DELETE-by-tag). The fake registry.New() double stores manifests under
	// both the tag key and the digest key independently and only clears the
	// key it's asked to delete, so it still serves the tag after this call --
	// unlike a real registry, where the tag is a pointer to the same
	// content and is invalidated along with it. Assert directly on the
	// digest key DeleteManifest deletes, since the tag key isn't a reliable
	// signal against this fake.
	raw, err := m.MarshalOCI()
	require.NoError(t, err)
	digest, _, err := v1.SHA256(bytes.NewReader(raw))
	require.NoError(t, err)

	repo, err := name.NewRepository(host + "/acme/snapshots")
	require.NoError(t, err)
	_, err = remote.Get(repo.Digest(digest.String()))
	require.Error(t, err)
}

func TestDeleteManifest_NotFound(t *testing.T) {
	host := newTestRegistry(t)
	ctx := context.Background()

	ref := host + "/acme/snapshots:never-pushed-20260801120000"

	err := DeleteManifest(ctx, ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), ref)
}

func TestDeleteManifest_RefusedDelete(t *testing.T) {
	ctx := context.Background()

	// A canned manifest body so the HEAD DeleteManifest issues to resolve the
	// tag to a digest gets a real, self-consistent Content-Type,
	// Content-Length, and Docker-Content-Digest. Without all three,
	// go-containerregistry's headManifest (remote/fetcher.go:256-270) errors
	// out before DeleteManifest ever reaches the DELETE call this test
	// exists to exercise, which would make the test pass vacuously.
	cannedManifest := []byte(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`,
	)
	cannedDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(cannedManifest))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "manifests") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			errResp := map[string]any{
				"errors": []map[string]any{
					{
						"code":    "UNSUPPORTED",
						"message": "The operation is unsupported.",
						"detail":  "DELETE is not supported for manifest references",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(errResp)
			return
		}

		if r.Method == http.MethodHead && strings.Contains(r.URL.Path, "manifests") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Content-Length", strconv.Itoa(len(cannedManifest)))
			w.Header().Set("Docker-Content-Digest", cannedDigest)
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodHead ||
			(r.Method == http.MethodGet && strings.Contains(r.URL.Path, "v2")) {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	ref := fmt.Sprintf("%s/acme/snapshots:tag-20260801120000", host)

	err := DeleteManifest(ctx, ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), ref)
	require.Contains(
		t,
		err.Error(),
		"UNSUPPORTED",
		"error should surface the registry's refused-delete response, not fail earlier at digest resolution",
	)
}

package microsandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const (
	numericNamedGroup  = "1000:developers"
	accountLinkTarget  = "other"
	escapingLinkTarget = "../../outside"
	developerGroup     = "developers:x:1001:\n"
)

func (s *imageUserSuite) TestUnsafeAccountLinkDoesNotExposeLowerLayer() {
	layer := s.headerLayer(&tar.Header{
		Name: passwdPath, Typeflag: tar.TypeSymlink, Linkname: escapingLinkTarget,
	})
	prepared := s.accountImage(s.layer(map[string]string{passwdPath: snapshotPasswd}), layer)
	_, err := ownerFromImage(context.Background(), prepared, testUser)
	s.ErrorContains(err, "not a regular account file")
}

func (s *imageUserSuite) TestOpaqueAccountDirectory() {
	for _, marker := range []string{".wh.etc", ".wh..wh..opq", "etc/.wh..wh..opq"} {
		s.Run(marker, func() {
			base := s.layer(map[string]string{passwdPath: snapshotPasswd, groupPath: "old:x:99:\n"})
			prepared := s.accountImage(base, s.layer(map[string]string{marker: ""}))
			_, err := ownerFromImage(context.Background(), prepared, testUser)
			s.ErrorContains(err, "not found")
			prepared = s.accountImage(
				base,
				s.layer(map[string]string{marker: "", passwdPath: snapshotPasswd}),
			)
			owner, err := ownerFromImage(
				context.Background(),
				prepared,
				testUser,
			)
			s.Require().NoError(err)
			s.Equal(mountOwner{2000, 2001}, *owner)
			_, err = ownerFromImage(context.Background(), prepared, "vscode:old")
			s.ErrorContains(err, "not found")
		})
	}
}

func (s *imageUserSuite) TestLowerAccountLinkHiddenByRegularReplacement() {
	layer := s.headerLayer(&tar.Header{
		Name: passwdPath, Typeflag: tar.TypeSymlink, Linkname: escapingLinkTarget,
	})
	prepared := s.accountImage(layer, s.layer(map[string]string{passwdPath: snapshotPasswd}))
	owner, err := ownerFromImage(context.Background(), prepared, testUser)
	s.Require().NoError(err)
	s.Equal(mountOwner{2000, 2001}, *owner)
}

func (s *imageUserSuite) TestAccountDirectoryLinkRejected() {
	layer := s.headerLayer(&tar.Header{
		Name: "etc", Typeflag: tar.TypeSymlink, Linkname: accountLinkTarget,
	})
	prepared := s.accountImage(s.layer(map[string]string{passwdPath: snapshotPasswd}), layer)
	_, err := ownerFromImage(context.Background(), prepared, testUser)
	s.ErrorContains(err, "not a regular account directory")
}

func (s *imageUserSuite) TestUnrelatedAccountFileDoesNotBlockResolution() {
	for _, user := range []string{testUser, numericNamedGroup} {
		s.Run(user, func() {
			unrelated := groupPath
			expected := mountOwner{2000, 2001}
			if user != testUser {
				unrelated = passwdPath
				expected = mountOwner{1000, 1001}
			}
			base := s.layer(
				map[string]string{passwdPath: snapshotPasswd, groupPath: developerGroup},
			)
			for _, bad := range []v1.Layer{
				s.headerLayer(&tar.Header{Name: unrelated, Typeflag: tar.TypeSymlink, Linkname: accountLinkTarget}),
				s.layer(map[string]string{unrelated: strings.Repeat("x", maxAccountFileSize+1)}),
			} {
				prepared := s.accountImage(base, bad)
				owner, err := ownerFromImage(context.Background(), prepared, user)
				s.Require().NoError(err)
				s.Equal(expected, *owner)
			}
		})
	}
}

func (s *imageUserSuite) TestInvalidFinalAccountPreservesExistingSandbox() {
	image := s.accountImage(
		s.layer(map[string]string{passwdPath: snapshotPasswd}),
		s.headerLayer(&tar.Header{
			Name: passwdPath, Typeflag: tar.TypeSymlink, Linkname: escapingLinkTarget,
		}),
	)
	client := &accountImageClient{fakeClient: newFakeClient(), image: image}
	client.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
	d := newDriver(client, nil, specDefaults{})
	err := d.RunDevContainer(context.Background(), wsID, &driver.RunOptions{
		Image: imgX, RemoteUser: testUser, AllowRecreate: true,
		WorkspaceMount: &config.Mount{Source: testBindSrc, Target: testBindDst},
	})
	s.ErrorContains(err, "not a regular account file")
	s.Empty(client.calls)
	s.True(client.info[wsName].Running)
	s.Empty(client.created)
}

//nolint:gosec // Synthetic account fixture contains no credentials.
func (s *imageUserSuite) TestRootWithGroupUsesImageAccount() {
	img := s.accountImage(s.layer(map[string]string{
		passwdPath: "root:x:42:43:root:/root:/bin/sh\n", groupPath: developerGroup,
	}))
	for _, user := range []string{"root:developers", "root:1001"} {
		owner, err := ownerFromImage(context.Background(), img, user)
		s.Require().NoError(err)
		s.Equal(mountOwner{42, 1001}, *owner)
	}
	_, err := ownerFromImage(context.Background(), empty.Image, "root:1001")
	s.ErrorContains(err, "not found in final image /etc/passwd")
}

func (s *imageUserSuite) TestCanceledExplicitOwner() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (filesystemUserResolver{}).ResolveImage(ctx, nil, testNumericIdentity)
	s.ErrorIs(err, context.Canceled)
}

func (s *imageUserSuite) TestCancellationClosesBlockedLayer() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := net.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	started := make(chan struct{}, 1)
	img := accountLayerImage{Image: empty.Image, layer: blockingAccountLayer{
		reader: &startedAccountReader{Conn: reader, started: started},
	}}
	done := make(chan error, 1)
	go func() { _, err := ownerFromImage(ctx, img, testUser); done <- err }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		s.FailNow("account layer read did not start")
	}
	cancel()
	select {
	case err := <-done:
		s.ErrorIs(err, context.Canceled)
	case <-time.After(10 * time.Second):
		s.FailNow("cancelled account layer remained blocked")
	}
}

func (s *imageUserSuite) accountImage(layers ...v1.Layer) v1.Image {
	img, err := mutate.AppendLayers(empty.Image, layers...)
	s.Require().NoError(err)
	return img
}

func (s *imageUserSuite) headerLayer(header *tar.Header) v1.Layer {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	s.Require().NoError(writer.WriteHeader(header))
	s.Require().NoError(writer.Close())
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	})
	s.Require().NoError(err)
	return layer
}

type accountImageClient struct {
	*fakeClient
	image v1.Image
}

func (client *accountImageClient) PrepareImage(
	_ context.Context,
	ref string,
	_ bool,
) (*preparedImage, error) {
	return &preparedImage{reference: ref, image: client.image, cleanup: func() {}}, nil
}

type accountLayerImage struct {
	v1.Image
	layer v1.Layer
}

func (img accountLayerImage) Layers() ([]v1.Layer, error) { return []v1.Layer{img.layer}, nil }

type blockingAccountLayer struct {
	v1.Layer
	reader io.ReadCloser
}

func (layer blockingAccountLayer) Uncompressed() (io.ReadCloser, error) { return layer.reader, nil }

type startedAccountReader struct {
	net.Conn
	started chan<- struct{}
}

func (reader *startedAccountReader) Read(buf []byte) (int, error) {
	select {
	case reader.started <- struct{}{}:
	default:
	}
	return reader.Conn.Read(buf)
}

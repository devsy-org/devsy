package microsandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/suite"
)

type imageUserSuite struct{ suite.Suite }

func TestImageUser(t *testing.T) { suite.Run(t, new(imageUserSuite)) }

func (s *imageUserSuite) TestResolution() {
	img, err := mutate.AppendLayers(empty.Image, s.layer(map[string]string{
		passwdPath: "invalid-row\nroot:x:0:0:root:/root:/bin/sh\nvscode:x:1000:1002:dev:/home/vscode:/bin/sh\n",
		groupPath:  "developers:x:1001:vscode\n",
	}))
	s.Require().NoError(err)
	for _, tt := range []struct {
		user string
		want mountOwner
	}{
		{rootUser, mountOwner{}},
		{"0", mountOwner{}},
		{testUser, mountOwner{1000, 1002}},
		{"1000", mountOwner{1000, 1002}},
		{testNumericIdentity, mountOwner{1000, 1001}},
		{"vscode:developers", mountOwner{1000, 1001}},
		{"vscode:1003", mountOwner{1000, 1003}},
		{"1000:developers", mountOwner{1000, 1001}},
	} {
		s.Run(tt.user, func() {
			owner, err := ownerFromImage(context.Background(), img, tt.user)
			s.Require().NoError(err)
			s.Equal(tt.want, *owner)
		})
	}
	for _, user := range []string{
		"missing", "1009", "vscode:missing", "4294967296:1", "vscode:4294967296", "", "vscode:", "1:2:3",
	} {
		s.Run(
			user,
			func() { _, err := ownerFromImage(context.Background(), img, user); s.Error(err) },
		)
	}
}

func (s *imageUserSuite) TestMissingAccounts() {
	owner, err := ownerFromImage(context.Background(), empty.Image, testNumericIdentity)
	s.Require().NoError(err)
	s.Equal(mountOwner{1000, 1001}, *owner)
	_, err = ownerFromImage(context.Background(), empty.Image, testUser)
	s.ErrorContains(err, "not found in final image /etc/passwd")
}

//nolint:gosec // Synthetic account rows contain no credentials.
func (s *imageUserSuite) TestFinalLayerOverridesAndWhiteouts() {
	base := s.layer(
		map[string]string{
			passwdPath: "vscode:x:10:11:dev:/home/vscode:/bin/sh\n",
			groupPath:  "old:x:99:\n",
		},
	)
	final := s.layer(
		map[string]string{
			passwdPath:      "vscode:x:2000:2001:dev:/home/vscode:/bin/sh\n",
			"etc/.wh.group": "",
		},
	)
	img, err := mutate.AppendLayers(empty.Image, base, final)
	s.Require().NoError(err)
	owner, err := ownerFromImage(context.Background(), img, testUser)
	s.Require().NoError(err)
	s.Equal(mountOwner{2000, 2001}, *owner)
	_, err = ownerFromImage(context.Background(), img, "vscode:old")
	s.ErrorContains(err, "not found")
}

func (s *imageUserSuite) TestMalformedAccount() {
	for _, passwd := range []string{
		"vscode:x:no:1000:dev:/home/vscode:/bin/sh\n",
		"vscode:x:4294967296:1000:dev:/home/vscode:/bin/sh\n",
	} {
		_, err := ownerFromAccounts(testUser, passwd, "")
		s.ErrorContains(err, "invalid account IDs")
	}
}

func (s *imageUserSuite) TestBoundedAccountFile() {
	img, err := mutate.AppendLayers(
		empty.Image,
		s.layer(map[string]string{passwdPath: strings.Repeat("x", maxAccountFileSize+1)}),
	)
	s.Require().NoError(err)
	_, err = ownerFromImage(context.Background(), img, testUser)
	s.ErrorContains(err, "size limit")
}

func (s *imageUserSuite) TestCanceledExtraction() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ownerFromImage(ctx, empty.Image, testUser)
	s.ErrorIs(err, context.Canceled)
}

//nolint:gosec // Synthetic account archive and executable fixture under TempDir.
func (s *imageUserSuite) TestLocalFinalImageUsesConfiguredCLI() {
	if runtime.GOOS == "windows" {
		s.T().Skip("fixture executable requires a POSIX shell")
	}
	img, err := mutate.AppendLayers(
		empty.Image,
		s.layer(map[string]string{passwdPath: "vscode:x:2000:2001:dev:/home/vscode:/bin/sh\n"}),
	)
	s.Require().NoError(err)
	cfg, err := img.ConfigFile()
	s.Require().NoError(err)
	cfg.Config.User = testUser
	img, err = mutate.ConfigFile(img, cfg)
	s.Require().NoError(err)
	dir := s.T().TempDir()
	archive := filepath.Join(dir, "image.tar")
	tag, err := name.NewTag("final-image:latest")
	s.Require().NoError(err)
	s.Require().NoError(tarball.WriteToFile(archive, tag, img))
	argsFile := filepath.Join(dir, "args")
	s.T().Setenv("DEVSY_TEST_ARCHIVE", archive)
	s.T().Setenv("DEVSY_TEST_ARGS", argsFile)
	executable := filepath.Join(dir, "configured-cli")
	script := `#!/bin/sh
if [ "$1" = image ]; then exit 0; fi
printf '%s\n' "$@" > "$DEVSY_TEST_ARGS"
cat "$DEVSY_TEST_ARCHIVE"
`
	s.Require().NoError(os.WriteFile(executable, []byte(script), 0o700))
	for _, built := range []bool{true, false} {
		owner, err := (filesystemUserResolver{dockerPath: executable}).Resolve(
			context.Background(), "final-image:latest", built, testUser,
		)
		s.Require().NoError(err)
		s.Equal(mountOwner{2000, 2001}, *owner)
	}
	args, err := os.ReadFile(argsFile)
	s.Require().NoError(err)
	s.Equal("save\nfinal-image:latest\n", string(args))
	d := newDriver(newFakeClient(), nil, specDefaults{})
	d.dockerPath = executable
	details, err := d.InspectImage(context.Background(), "final-image:latest")
	s.Require().NoError(err)
	s.Equal(testUser, details.Config.User)
}

//nolint:gosec // Test-created executable and archive paths under TempDir.
func (s *imageUserSuite) TestLocalImportUsesConfiguredCLIAndDrainsArchive() {
	if runtime.GOOS == "windows" {
		s.T().Skip("fixture executables require a POSIX shell")
	}
	dir := s.T().TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	content := strings.Repeat("archive data", 100000)
	s.Require().NoError(os.WriteFile(source, []byte(content), 0o600))
	s.T().Setenv("DEVSY_TEST_ARCHIVE", source)
	s.T().Setenv("DEVSY_TEST_TARGET", target)
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	saveCLI := filepath.Join(dir, "save-cli")
	saveScript := "#!/bin/sh\nif [ \"$1\" = image ]; then exit 0; fi\ncat \"$DEVSY_TEST_ARCHIVE\"\n"
	s.Require().NoError(os.WriteFile(saveCLI, []byte(saveScript), 0o700))
	s.Require().
		NoError(os.WriteFile(filepath.Join(dir, "msb"), []byte("#!/bin/sh\ncat > \"$DEVSY_TEST_TARGET\"\n"), 0o700))
	for _, built := range []bool{true, false} {
		s.Require().
			NoError((cliClient{dockerPath: saveCLI}).EnsureImage(context.Background(), "final-image:latest", built))
	}
	data, err := os.ReadFile(target)
	s.Require().NoError(err)
	s.Equal(content, string(data))
}

func (s *imageUserSuite) layer(files map[string]string) v1.Layer {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for name, data := range files {
		s.Require().
			NoError(writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}))
		_, err := writer.Write([]byte(data))
		s.Require().NoError(err)
	}
	s.Require().NoError(writer.Close())
	layer, err := tarball.LayerFromOpener(
		func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf.Bytes())), nil },
	)
	s.Require().NoError(err)
	return layer
}

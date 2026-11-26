package microsandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/suite"
)

//nolint:gosec // Synthetic account fixture contains no credentials.
const snapshotPasswd = "vscode:x:2000:2001:dev:/home/vscode:/bin/sh\n"

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
			passwdPath:      snapshotPasswd,
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
	s.requirePOSIXShell()
	img, err := mutate.AppendLayers(
		empty.Image,
		s.layer(map[string]string{passwdPath: snapshotPasswd}),
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
		resolver := filesystemUserResolver{dockerPath: executable}
		snapshot, cleanup, err := resolver.openImage(
			context.Background(),
			"final-image:latest",
			built,
		)
		s.Require().NoError(err)
		defer cleanup()
		owner, err := resolver.ResolveImage(context.Background(), snapshot, testUser)
		s.Require().NoError(err)
		s.Equal(mountOwner{2000, 2001}, *owner)
	}
	args, err := os.ReadFile(argsFile)
	s.Require().NoError(err)
	s.Equal("save\nfinal-image:latest\n", string(args))
}

//nolint:gosec // Synthetic archives and executable fixtures under TempDir.
func (s *imageUserSuite) TestSnapshotSurvivesRetagBeforeImport() {
	s.requirePOSIXShell()
	for _, source := range []string{"local", "registry"} {
		s.Run(source, func() {
			ctx := context.Background()
			original := s.snapshotImage(snapshotPasswd)
			replacement := s.snapshotImage("vscode:x:3000:3001:dev:/home/vscode:/bin/sh\n")
			dir := s.T().TempDir()
			archive := filepath.Join(dir, "source.tar")
			imported := filepath.Join(dir, "imported.tar")
			aliasFile := filepath.Join(dir, "alias")
			s.T().Setenv("DEVSY_TEST_ARCHIVE", archive)
			s.T().Setenv("DEVSY_TEST_IMPORTED", imported)
			s.T().Setenv("DEVSY_TEST_ALIAS", aliasFile)
			s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cliPath := filepath.Join(dir, "configured-cli")
			script := "#!/bin/sh\nif [ \"$1\" = image ]; then exit 0; fi\ncat \"$DEVSY_TEST_ARCHIVE\"\n"
			ref := "final-image:latest"
			if source == "registry" {
				server := httptest.NewServer(registry.New())
				defer server.Close()
				ref = strings.TrimPrefix(server.URL, "http://") + "/final-image:latest"
				script = "#!/bin/sh\nexit 1\n"
			}
			s.Require().NoError(os.WriteFile(cliPath, []byte(script), 0o700))
			tag, err := name.NewTag(ref)
			s.Require().NoError(err)
			writeImage := func(img v1.Image) {
				if source == "local" {
					s.Require().NoError(tarball.WriteToFile(archive, tag, img))
				} else {
					s.Require().NoError(remote.Write(tag, img))
				}
			}
			writeImage(original)
			client := cliClient{dockerPath: cliPath}
			prepared, err := client.PrepareImage(ctx, ref, false)
			s.Require().NoError(err)
			defer prepared.cleanup()
			writeImage(replacement)
			newer, err := client.PrepareImage(ctx, ref, false)
			s.Require().NoError(err)
			defer newer.cleanup()
			s.NotEqual(prepared.reference, newer.reference)
			owner, err := (filesystemUserResolver{}).ResolveImage(ctx, prepared.image, testUser)
			s.Require().NoError(err)
			s.Equal(mountOwner{2000, 2001}, *owner)
			s.writeSnapshotImportCLI(dir)
			s.Require().NoError(client.EnsureImage(ctx, prepared))
			importedImage, err := tarball.ImageFromPath(imported, nil)
			s.Require().NoError(err)
			importedOwner, err := ownerFromImage(ctx, importedImage, testUser)
			s.Require().NoError(err)
			s.Equal(*owner, *importedOwner)
			alias, err := os.ReadFile(aliasFile)
			s.Require().NoError(err)
			s.Equal(prepared.reference, string(alias))
		})
	}
}

//nolint:gosec // Test-created executable under TempDir.
func (s *imageUserSuite) TestStreamImportReapsEarlyConsumerExit() {
	s.requirePOSIXShell()
	dir := s.T().TempDir()
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\necho rejected >&2\nexit 1\n"
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "msb"), []byte(script), 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := loadImageSnapshot(ctx, "devsy-msb-image:test", s.snapshotImage(snapshotPasswd))
	s.ErrorContains(err, "rejected")
	s.NoError(ctx.Err(), "an early consumer exit must not leave a blocked writer")
}

//nolint:gosec // Test-created executable under TempDir.
func (s *imageUserSuite) TestStreamImportHonorsCancellation() {
	s.requirePOSIXShell()
	dir := s.T().TempDir()
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nexec sleep 60\n"
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "msb"), []byte(script), 0o700))
	img := s.snapshotImage(snapshotPasswd)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.ErrorIs(loadImageSnapshot(ctx, "devsy-msb-image:test", img), context.DeadlineExceeded)
}

func (s *imageUserSuite) TestRegistryMetadataWithoutCLI() {
	s.requirePOSIXShell()
	server := httptest.NewServer(registry.New())
	defer server.Close()
	ref, err := name.NewTag(strings.TrimPrefix(server.URL, "http://") + "/metadata:latest")
	s.Require().NoError(err)
	img := s.snapshotImage(snapshotPasswd)
	img, err = mutate.Config(img, v1.Config{User: testUser})
	s.Require().NoError(err)
	s.Require().NoError(remote.Write(ref, img))
	d := newDriver(newFakeClient(), nil, specDefaults{})
	dir := s.T().TempDir()
	failingCLI := filepath.Join(dir, "failing-cli")
	// #nosec G306 -- executable fixture under TempDir.
	s.Require().NoError(os.WriteFile(failingCLI, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	for _, cli := range []string{filepath.Join(dir, "missing-cli"), failingCLI} {
		d.dockerPath = cli
		details, err := d.InspectImage(context.Background(), ref.Name())
		s.Require().NoError(err)
		s.Equal(testUser, details.Config.User)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.InspectImage(ctx, ref.Name())
	s.ErrorIs(err, context.Canceled)
}

//nolint:gosec // Test-created executable under TempDir.
func (s *imageUserSuite) TestInspectCachedImageConfig() {
	s.requirePOSIXShell()
	cfg := v1.Config{
		User: testUser,
		Env:  []string{"EXAMPLE=value"},
		Labels: map[string]string{
			"example": "value",
		},
		Entrypoint: []string{"/bin/sh"},
		Cmd:        []string{"-l"},
	}
	dir := s.T().TempDir()
	configFile, argsFile := filepath.Join(dir, "config.json"), filepath.Join(dir, "args")
	configJSON, err := json.Marshal(cfg)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(configFile, configJSON, 0o600))
	s.T().Setenv("DEVSY_TEST_CONFIG", configFile)
	s.T().Setenv("DEVSY_TEST_ARGS", argsFile)
	executable := filepath.Join(dir, "configured-cli")
	script := `#!/bin/sh
[ "$1" = image ] && [ "$2" = inspect ] || exit 1
printf '%s\n' "$@" > "$DEVSY_TEST_ARGS"
cat "$DEVSY_TEST_CONFIG"
`
	s.Require().NoError(os.WriteFile(executable, []byte(script), 0o700))
	d := newDriver(newFakeClient(), nil, specDefaults{})
	d.dockerPath = executable
	details, err := d.InspectImage(context.Background(), "final-image:latest")
	s.Require().NoError(err)
	s.Equal(cfg.User, details.Config.User)
	s.Equal(cfg.Env, details.Config.Env)
	s.Equal(cfg.Labels, details.Config.Labels)
	s.Equal(cfg.Entrypoint, details.Config.Entrypoint)
	s.Equal(cfg.Cmd, details.Config.Cmd)
	args, err := os.ReadFile(argsFile)
	s.Require().NoError(err)
	s.Equal("image\ninspect\n--format\n{{json .Config}}\nfinal-image:latest\n", string(args))
	s.Require().NoError(os.WriteFile(configFile, []byte("invalid JSON"), 0o600))
	_, err = d.InspectImage(context.Background(), "final-image:latest")
	s.ErrorContains(err, "parse cached image config")
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

func (s *imageUserSuite) snapshotImage(accounts string) v1.Image {
	padding := make([]byte, 1024*1024)
	_, err := rand.Read(padding)
	s.Require().NoError(err)
	img, err := mutate.AppendLayers(empty.Image, s.layer(map[string]string{
		passwdPath: accounts, "padding.bin": string(padding),
	}))
	s.Require().NoError(err)
	return img
}

//nolint:gosec // Test-created executable under TempDir.
func (s *imageUserSuite) writeSnapshotImportCLI(dir string) {
	msb := `#!/bin/sh
[ "$1" = load ] || exit 1
cat > "$DEVSY_TEST_IMPORTED"
printf '%s' "$3" > "$DEVSY_TEST_ALIAS"
`
	s.T().Setenv("TMPDIR", filepath.Join(dir, "unavailable-temp"))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "msb"), []byte(msb), 0o700))
}

func (s *imageUserSuite) requirePOSIXShell() {
	if runtime.GOOS == "windows" {
		s.T().Skip("fixture executable requires a POSIX shell")
	}
}

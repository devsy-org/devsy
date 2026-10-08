package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceIgnorePresence(t *testing.T) {
	root := t.TempDir()
	policy, err := loadWorkspaceIgnore(root)
	require.NoError(t, err)
	require.False(t, policy.present)
	require.NoError(t, os.WriteFile(filepath.Join(root, pkgconfig.IgnoreFileName), nil, 0o600))
	policy, err = loadWorkspaceIgnore(root)
	require.NoError(t, err)
	require.True(t, policy.present)
	require.Empty(t, policy.patterns)
}

func TestWorkspaceIgnoreErrorsStopAllTransfers(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			root := t.TempDir()
			ignore := filepath.Join(root, pkgconfig.IgnoreFileName)
			if unreadable {
				require.NoError(t, os.Mkdir(ignore, 0o700))
			} else {
				require.NoError(t, os.WriteFile(ignore, []byte("[\n"), 0o600))
			}
			assertWorkspacePolicyStopsTransfers(t, root)
		})
	}
}

func TestWorkspaceMountIdentityAndSetupValidation(t *testing.T) {
	results := []*config.Result{
		nil,
		{},
		{SubstitutionContext: &config.SubstitutionContext{}},
		{MergedConfig: &config.MergedDevContainerConfig{}},
	}
	for _, result := range results {
		_, err := newSetupServer(result)
		require.ErrorContains(t, err, "invalid setup result")
	}
	info := newSetupInfo(t)
	workspace := config.GetWorkspaceMount(info)
	info.MergedConfig.Mounts = append(
		info.MergedConfig.Mounts,
		workspace,
		&config.Mount{Type: testBindMountType, Source: workspace.Source, Target: "/second"},
	)
	server, err := newSetupServer(info)
	require.NoError(t, err)
	require.Len(t, server.mounts, 3)
	require.Nil(t, server.workspace)
	entries := streamMountEntries(
		t,
		server,
		info.MergedConfig.Mounts[len(info.MergedConfig.Mounts)-1],
	)
	require.Contains(t, entries, "web/node_modules/lib/index.js")
	bad := New(
		WithMounts([]*config.Mount{workspace}),
		WithWorkspaceMount(
			&config.Mount{Type: testBindMountType, Source: workspace.Source, Target: "/wrong"},
		),
	)
	require.ErrorContains(t, bad.validateMountRoles(), "authorized mount set")
}

func TestExactGeneratedArtifactProtection(t *testing.T) {
	info := newSetupInfo(t)
	mount := config.GetWorkspaceMount(info)
	generated := ".devcontainer/" + config.DevsyContextFeatureFolder + "/feature/install.sh"
	writeFiles(
		t,
		mount.Source,
		generated,
		"private/"+config.DevsyContextFeatureFolder+"/secret.key",
		".devcontainer/private.txt",
		"run.sh",
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(mount.Source, pkgconfig.IgnoreFileName),
			[]byte(".devcontainer/\n**/"+config.DevsyContextFeatureFolder+"/\n**/*.sh\n"),
			0o600,
		),
	)
	info.GeneratedBuildContext = filepath.Join(mount.Source, ".devcontainer")
	info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
		{
			Path:   filepath.Join(mount.Source, generated),
			SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(generated))),
		},
	}
	server, err := newSetupServer(info)
	require.NoError(t, err)
	entries := streamMountEntries(t, server, mount)
	require.Contains(t, entries, generated)
	require.NotContains(t, entries, ".devcontainer/private.txt")
	require.NotContains(t, entries, "private/"+config.DevsyContextFeatureFolder+"/secret.key")
	require.NotContains(t, entries, "run.sh")
	require.NoError(t, os.WriteFile(info.GeneratedBuildArtifacts[0].Path, []byte("changed"), 0o600))
	stream := &mockStreamMountServer{}
	require.ErrorContains(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
		"changed since build",
	)
	require.Zero(t, stream.content.Len())
}

func TestSnapshotWorkspacePolicyDoesNotFilterOtherMount(t *testing.T) {
	info := newSetupInfo(t)
	server, err := newSetupServer(info)
	require.NoError(t, err)
	other := info.MergedConfig.Mounts[0]
	writeFiles(t, other.Source, config.DevsyContextFeatureFolder+"/keep.txt")
	stream := &fakeStreamServer{}
	require.NoError(t, server.StreamSnapshotVolumes(&tunnel.Empty{}, stream))
	names := tarEntryNames(t, stream.chunks)
	require.Contains(t, names, "home/user/.other/node_modules/lib/index.js")
	require.Contains(t, names, "home/user/.other/"+config.DevsyContextFeatureFolder+"/keep.txt")
	require.NotContains(t, names, "workspaces/project/web/node_modules/lib/index.js")
	require.Contains(t, names, "workspaces/project/src/main.go")
}

func TestGeneratedArtifactSymlinkFailsBeforeUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges")
	}
	info := newSetupInfo(t)
	mount := config.GetWorkspaceMount(info)
	external := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(external, []byte("external"), 0o600))
	directory := filepath.Join(mount.Source, config.DevsyContextFeatureFolder)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	leaf := filepath.Join(directory, "Dockerfile-without-features")
	require.NoError(t, os.Symlink(external, leaf))
	info.GeneratedBuildContext = mount.Source
	info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
		{Path: leaf, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("external")))},
	}
	server, err := newSetupServer(info)
	require.NoError(t, err)
	stream := &mockStreamMountServer{}
	require.ErrorContains(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
		"symlink",
	)
	require.Zero(t, stream.content.Len())
}

func TestGeneratedArtifactManifestCannotAuthorizeOtherPaths(t *testing.T) {
	for _, outsideWorkspace := range []bool{false, true} {
		t.Run(fmt.Sprint(outsideWorkspace), func(t *testing.T) {
			info := newSetupInfo(t)
			mount := config.GetWorkspaceMount(info)
			root := mount.Source
			if outsideWorkspace {
				root = t.TempDir()
			}
			artifact := filepath.Join(root, "private.txt")
			require.NoError(t, os.WriteFile(artifact, []byte("private"), 0o600))
			info.GeneratedBuildContext = mount.Source
			info.GeneratedBuildArtifacts = []config.GeneratedBuildArtifact{
				{Path: artifact, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("private")))},
			}
			server, err := newSetupServer(info)
			require.NoError(t, err)
			stream := &mockStreamMountServer{}
			require.ErrorContains(
				t,
				server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
				"outside",
			)
			require.Zero(t, stream.content.Len())
		})
	}
}

func TestDanglingWorkspaceIgnoreStopsAllTransfers(t *testing.T) {
	root := t.TempDir()
	ignore := filepath.Join(root, pkgconfig.IgnoreFileName)
	if err := os.Symlink(filepath.Join(root, "missing-ignore-target"), ignore); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	_, err := loadWorkspaceIgnore(root)
	require.ErrorContains(t, err, "upload stopped")
	require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
	assertWorkspacePolicyStopsTransfers(t, root)
}

func assertWorkspacePolicyStopsTransfers(t *testing.T, root string) {
	t.Helper()
	mount := &config.Mount{Type: testBindMountType, Source: root, Target: "/workspace"}
	server := New(
		WithMounts([]*config.Mount{mount}),
		WithWorkspaceMount(mount),
		WithWorkspace(&provider2.Workspace{Source: provider2.WorkspaceSource{LocalFolder: root}}),
	)
	calls := []func(*mockStreamMountServer) error{
		func(s *mockStreamMountServer) error {
			return server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, s)
		},
		func(s *mockStreamMountServer) error { return server.StreamWorkspace(&tunnel.Empty{}, s) },
		func(s *mockStreamMountServer) error { return server.StreamSnapshotVolumes(&tunnel.Empty{}, s) },
	}
	for _, call := range calls {
		stream := &mockStreamMountServer{}
		err := call(stream)
		require.ErrorContains(t, err, "upload stopped")
		require.ErrorContains(t, err, pkgconfig.IgnoreFileName)
		require.Zero(t, stream.content.Len())
	}
}

func isolatedArtifactStaging(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	return dir
}

func stagedArtifactServer(t *testing.T, size int64) (*tunnelServer, string) {
	t.Helper()
	root := t.TempDir()
	relative := config.DevsyContextFeatureFolder + "/payload.bin"
	artifact := filepath.Join(root, relative)
	require.NoError(t, os.MkdirAll(filepath.Dir(artifact), 0o700))
	// #nosec G304 -- the file is a fixture under this test's temporary root.
	file, err := os.Create(artifact)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	mount := &config.Mount{Type: testBindMountType, Source: root, Target: "/workspace"}
	server := New(WithMounts([]*config.Mount{mount}), WithWorkspaceMount(mount))
	server.generatedBuildContext = root
	server.generatedBuildArtifacts = []config.GeneratedBuildArtifact{
		{Path: artifact, SHA256: fmt.Sprintf("%x", hash.Sum(nil))},
	}
	return server, relative
}

func TestGeneratedArtifactsStageLargeImmutableBodiesOnDisk(t *testing.T) {
	stagingDir := isolatedArtifactStaging(t)
	server, relative := stagedArtifactServer(t, 40*1024*1024)
	opts, cleanup, err := server.sourceTarOptions(
		context.Background(),
		server.generatedBuildContext,
		true,
		false,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	source := opts.ProtectedFiles[relative]
	reader, ok := source.Reader.(*io.SectionReader)
	require.True(t, ok, "large approved content must remain disk-backed")
	backing, _, size := reader.Outer()
	staged, ok := backing.(*os.File)
	require.True(t, ok)
	require.EqualValues(t, 40*1024*1024, size)
	require.Equal(t, stagingDir, filepath.Dir(staged.Name()))
	require.NoError(
		t,
		os.WriteFile(server.generatedBuildArtifacts[0].Path, []byte("changed"), 0o600),
	)
	hash := sha256.New()
	copied, err := io.Copy(hash, source.Reader)
	require.NoError(t, err)
	require.Equal(t, size, copied)
	require.Equal(t, server.generatedBuildArtifacts[0].SHA256, fmt.Sprintf("%x", hash.Sum(nil)))
	require.NoError(t, cleanup())
	require.NoFileExists(t, staged.Name())
}

func TestGeneratedArtifactStagingPreflightFailureCleansDisk(t *testing.T) {
	stagingDir := isolatedArtifactStaging(t)
	server, _ := stagedArtifactServer(t, 16)
	invalid := server.generatedBuildArtifacts[0]
	invalid.SHA256 = "changed-digest"
	server.generatedBuildArtifacts = append(server.generatedBuildArtifacts, invalid)
	_, _, err := server.sourceTarOptions(
		context.Background(),
		server.generatedBuildContext,
		true,
		false,
	)
	require.ErrorContains(t, err, "changed since build")
	entries, err := os.ReadDir(stagingDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

type generatedArtifactLifecycleStream struct {
	mockStreamMountServer
	ctx    context.Context
	onSend func() error
}

func (s *generatedArtifactLifecycleStream) Context() context.Context { return s.ctx }

func (s *generatedArtifactLifecycleStream) Send(chunk *tunnel.Chunk) error {
	if s.onSend != nil {
		return s.onSend()
	}
	return s.mockStreamMountServer.Send(chunk)
}

func TestGeneratedArtifactStagingReleasedAfterTransfer(t *testing.T) {
	for _, outcome := range []string{"success", "send failure", "cancellation"} {
		t.Run(outcome, func(t *testing.T) {
			stagingDir := isolatedArtifactStaging(t)
			server, _ := stagedArtifactServer(t, 16)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := &generatedArtifactLifecycleStream{ctx: ctx}
			switch outcome {
			case "send failure":
				stream.onSend = func() error { return errors.New("destination failed") }
			case "cancellation":
				stream.onSend = func() error { cancel(); return ctx.Err() }
			}
			err := server.StreamMount(
				&tunnel.StreamMountRequest{Mount: server.mounts[0].String()},
				stream,
			)
			if outcome == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			entries, err := os.ReadDir(stagingDir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestGeneratedArtifactCanceledPreflightCleansDisk(t *testing.T) {
	stagingDir := isolatedArtifactStaging(t)
	server, _ := stagedArtifactServer(t, 16)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := server.sourceTarOptions(ctx, server.generatedBuildContext, true, false)
	require.ErrorIs(t, err, context.Canceled)
	entries, err := os.ReadDir(stagingDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestGeneratedArtifactSnapshotDoesNotStageBodies(t *testing.T) {
	stagingDir := isolatedArtifactStaging(t)
	server, _ := stagedArtifactServer(t, 16)
	opts, cleanup, err := server.sourceTarOptions(
		context.Background(),
		server.generatedBuildContext,
		true,
		true,
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, cleanup()) }()
	require.Empty(t, opts.ProtectedFiles)
	entries, err := os.ReadDir(stagingDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestGeneratedSymlinkArchivesApprovedTargetAfterMutation(t *testing.T) {
	for _, target := range []string{"payload.bin", ".."} {
		t.Run(target, func(t *testing.T) {
			server, _ := stagedArtifactServer(t, 16)
			root := server.generatedBuildContext
			relative := config.DevsyContextFeatureFolder + "/link"
			if err := os.Symlink(target, filepath.Join(root, relative)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			server.generatedBuildArtifacts = []config.GeneratedBuildArtifact{
				{Path: filepath.Join(root, relative), LinkTarget: target},
			}
			opts, cleanup, err := server.sourceTarOptions(context.Background(), root, true, false)
			require.NoError(t, err)
			defer func() { require.NoError(t, cleanup()) }()
			require.NoError(t, os.Remove(filepath.Join(root, relative)))
			require.NoError(t, os.Symlink("changed-target", filepath.Join(root, relative)))
			opts.Excludes = []string{"**"}
			opts.Matcher = nil
			var archive bytes.Buffer
			require.NoError(t, extract.WriteTarWithOptions(&archive, root, opts))
			header, err := tar.NewReader(&archive).Next()
			require.NoError(t, err)
			require.Equal(t, byte(tar.TypeSymlink), header.Typeflag)
			require.Equal(t, target, header.Linkname)
		})
	}
}

func TestGeneratedSymlinkRejectsUnapprovedTargetsBeforeStreaming(t *testing.T) {
	for _, target := range []string{"changed-target", "../../outside", "/absolute/outside"} {
		t.Run(target, func(t *testing.T) {
			server, _ := stagedArtifactServer(t, 16)
			root := server.generatedBuildContext
			link := filepath.Join(root, config.DevsyContextFeatureFolder, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			approved := target
			if target == "changed-target" {
				approved = "payload.bin"
			}
			server.generatedBuildArtifacts = []config.GeneratedBuildArtifact{
				{Path: link, LinkTarget: approved},
			}
			stream := &mockStreamMountServer{}
			err := server.StreamMount(
				&tunnel.StreamMountRequest{Mount: server.mounts[0].String()},
				stream,
			)
			require.Error(t, err)
			require.Zero(t, stream.content.Len())
		})
	}
}

func TestGeneratedSnapshotPathsAreLiteral(t *testing.T) {
	for _, contextName := range []string{"!nested", "[nested]"} {
		t.Run(contextName, func(t *testing.T) {
			server, relative := stagedArtifactServer(t, 16)
			root := server.generatedBuildContext
			nested := filepath.Join(root, contextName)
			require.NoError(t, os.Mkdir(nested, 0o700))
			require.NoError(
				t,
				os.Rename(
					filepath.Join(root, config.DevsyContextFeatureFolder),
					filepath.Join(nested, config.DevsyContextFeatureFolder),
				),
			)
			server.generatedBuildContext = nested
			server.generatedBuildArtifacts[0].Path = filepath.Join(nested, relative)
			writeFiles(
				t,
				nested,
				"keep.txt",
				"private.txt",
				config.DevsyContextFeatureFolder+"/unmanifested.txt",
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(root, pkgconfig.IgnoreFileName),
					[]byte("**/private.txt\n"),
					0o600,
				),
			)
			opts, cleanup, err := server.sourceTarOptions(context.Background(), root, true, true)
			require.NoError(t, err)
			defer func() { require.NoError(t, cleanup()) }()
			var archive bytes.Buffer
			require.NoError(t, extract.WriteTarWithOptions(&archive, root, opts))
			names := tarEntryNames(t, [][]byte{archive.Bytes()})
			require.NotContains(t, names, contextName+"/"+relative)
			require.NotContains(t, names, contextName+"/private.txt")
			require.Contains(t, names, contextName+"/keep.txt")
			require.NotContains(
				t,
				names,
				contextName+"/"+config.DevsyContextFeatureFolder+"/unmanifested.txt",
			)
		})
	}
}

func TestGeneratedEmptyDirectoryKeepsOnlyItsHeader(t *testing.T) {
	server, _ := stagedArtifactServer(t, 16)
	root := server.generatedBuildContext
	relative := config.DevsyContextFeatureFolder + "/required-empty"
	directory := filepath.Join(root, relative)
	require.NoError(t, os.Mkdir(directory, 0o700))
	server.generatedBuildArtifacts = append(server.generatedBuildArtifacts,
		config.GeneratedBuildArtifact{Path: directory, Directory: true})
	var baseline bytes.Buffer
	require.NoError(t, extract.WriteTarWithOptions(&baseline, root, extract.TarOptions{}))
	require.Contains(t, tarEntryNames(t, [][]byte{baseline.Bytes()}), relative)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, pkgconfig.IgnoreFileName),
			[]byte(config.DevsyContextFeatureFolder+"/\n"),
			0o600,
		),
	)
	opts, cleanup, err := server.sourceTarOptions(context.Background(), root, true, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, cleanup()) }()
	writeFiles(t, directory, "later-secret.txt")
	var archive bytes.Buffer
	require.NoError(t, extract.WriteTarWithOptions(&archive, root, opts))
	names := tarEntryNames(t, [][]byte{archive.Bytes()})
	require.Contains(t, names, relative)
	require.NotContains(t, names, relative+"/later-secret.txt")
}

func TestGeneratedDirectorySnapshotExcludesContextResidue(t *testing.T) {
	server, _ := stagedArtifactServer(t, 16)
	root := server.generatedBuildContext
	relative := config.DevsyContextFeatureFolder + "/required-empty"
	directory := filepath.Join(root, relative)
	require.NoError(t, os.Mkdir(directory, 0o700))
	server.generatedBuildArtifacts = append(server.generatedBuildArtifacts,
		config.GeneratedBuildArtifact{Path: directory, Directory: true})
	writeFiles(t, directory, "later-user-file.txt")
	opts, cleanup, err := server.sourceTarOptions(context.Background(), root, true, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, cleanup()) }()
	require.Empty(t, opts.ProtectedDirectories)
	var archive bytes.Buffer
	require.NoError(t, extract.WriteTarWithOptions(&archive, root, opts))
	require.NotContains(
		t,
		tarEntryNames(t, [][]byte{archive.Bytes()}),
		relative+"/later-user-file.txt",
	)
}

func TestGeneratedDirectoryRejectsChangedTypeBeforeStreaming(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			server, _ := stagedArtifactServer(t, 16)
			root := server.generatedBuildContext
			directory := filepath.Join(root, config.DevsyContextFeatureFolder, "required-empty")
			if kind == "file" {
				require.NoError(t, os.WriteFile(directory, nil, 0o600))
			} else if err := os.Symlink(t.TempDir(), directory); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			server.generatedBuildArtifacts = []config.GeneratedBuildArtifact{
				{Path: directory, Directory: true},
			}
			stream := &mockStreamMountServer{}
			err := server.StreamMount(
				&tunnel.StreamMountRequest{Mount: server.mounts[0].String()},
				stream,
			)
			require.ErrorContains(t, err, "no longer a directory")
			require.Zero(t, stream.content.Len())
		})
	}
}

func TestSerializedBuildContextExcludesSnapshotResidue(t *testing.T) {
	for _, contextName := range []string{".", ".devcontainer"} {
		t.Run(contextName, func(t *testing.T) {
			info := newSetupInfo(t)
			mount := config.GetWorkspaceMount(info)
			contextRoot := filepath.Join(mount.Source, contextName)
			info.DevContainerConfigWithPath = &config.DevContainerConfigWithPath{
				Config: &config.DevContainerConfig{
					Origin: filepath.Join(contextRoot, "devcontainer.json"),
				},
				Path: filepath.ToSlash(filepath.Join(contextName, "devcontainer.json")),
			}
			info.SubstitutionContext.LocalWorkspaceFolder = mount.Source
			data, err := json.Marshal(info)
			require.NoError(t, err)
			var persisted config.Result
			require.NoError(t, json.Unmarshal(data, &persisted))
			require.Empty(t, persisted.GeneratedBuildArtifacts)
			require.Empty(t, persisted.GeneratedBuildContext)
			writeFiles(t, contextRoot, config.DevsyContextFeatureFolder+"/residual-secret")
			unrelated := "unrelated/" + config.DevsyContextFeatureFolder + "/keep"
			writeFiles(t, mount.Source, unrelated)
			other := persisted.MergedConfig.Mounts[0]
			writeFiles(t, other.Source, config.DevsyContextFeatureFolder+"/keep")
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(mount.Source, pkgconfig.IgnoreFileName),
					[]byte("!**/.devsy-internal/**\n"),
					0o600,
				),
			)
			server, err := newSetupServer(&persisted)
			require.NoError(t, err)
			stream := &mockStreamMountServer{}
			require.NoError(t, server.StreamSnapshotVolumes(&tunnel.Empty{}, stream))
			names := tarEntryNames(t, [][]byte{stream.content.Bytes()})
			residue := filepath.ToSlash(
				filepath.Join(
					"workspaces/project",
					contextName,
					config.DevsyContextFeatureFolder,
					"residual-secret",
				),
			)
			require.NotContains(t, names, residue)
			require.Contains(t, names, "workspaces/project/"+unrelated)
			require.Contains(t, names, "home/user/.other/"+config.DevsyContextFeatureFolder+"/keep")
		})
	}
}

func TestSnapshotBuildContextNeverGrantsUploadAuthority(t *testing.T) {
	info := newSetupInfo(t)
	mount := config.GetWorkspaceMount(info)
	contextRoot := filepath.Join(mount.Source, ".devcontainer")
	secret := ".devcontainer/" + config.DevsyContextFeatureFolder + "/secret"
	writeFiles(t, mount.Source, secret)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(mount.Source, pkgconfig.IgnoreFileName),
			[]byte(".devcontainer/\n"),
			0o600,
		),
	)
	server := New(
		WithMounts([]*config.Mount{mount}),
		WithWorkspaceMount(mount),
		WithSnapshotBuildContext(contextRoot),
	)
	require.NotContains(t, streamMountEntries(t, server, mount), secret)
}

func TestSnapshotExternalBuildContextDoesNotExcludeWorkspaceLookalike(t *testing.T) {
	root := t.TempDir()
	name := config.DevsyContextFeatureFolder + "/user-file"
	writeFiles(t, root, name)
	server := New(WithSnapshotBuildContext(t.TempDir()))
	opts, cleanup, err := server.sourceTarOptions(context.Background(), root, true, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, cleanup()) }()
	var archive bytes.Buffer
	require.NoError(t, extract.WriteTarWithOptions(&archive, root, opts))
	require.Contains(t, tarEntryNames(t, [][]byte{archive.Bytes()}), name)
}

func TestSnapshotContextErrorDoesNotBlockWorkspaceUpload(t *testing.T) {
	info := newSetupInfo(t)
	info.DevContainerConfigWithPath = &config.DevContainerConfigWithPath{
		Config: &config.DevContainerConfig{},
	}
	server, err := newSetupServer(info)
	require.NoError(t, err)
	mount := config.GetWorkspaceMount(info)
	require.Contains(t, streamMountEntries(t, server, mount), srcMainGo)
	stream := &mockStreamMountServer{}
	require.ErrorContains(
		t,
		server.StreamSnapshotVolumes(&tunnel.Empty{}, stream),
		"build context metadata",
	)
	require.Zero(t, stream.content.Len())
}

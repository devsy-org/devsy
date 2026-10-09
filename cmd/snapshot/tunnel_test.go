package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func TestNewLocalTunnelClient_StreamsMountContents(t *testing.T) {
	dir := snapshotIgnoreFixture(t)

	mounts := []*devcontainerconfig.Mount{
		{Type: testBindMountType, Source: dir, Target: "/workspaces/proj"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, cleanup, err := newLocalTunnelClient(
		ctx,
		mounts,
		mounts[0],
		filepath.Join(dir, ".devcontainer"),
	)
	require.NoError(t, err)
	defer cleanup()

	stream, err := client.StreamSnapshotVolumes(ctx, &tunnel.Empty{})
	require.NoError(t, err)

	names := snapshotArchiveNames(t, stream)
	require.Contains(t, names, "workspaces/proj/hello.txt")
	require.NotContains(t, names, "workspaces/proj/omit.txt")
	require.Contains(
		t,
		names,
		"workspaces/proj/"+devcontainerconfig.DevsyContextFeatureFolder+"/retained.txt",
	)
}

func snapshotIgnoreFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "omit.txt"), []byte("omitted"), 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, ".devsyignore"), []byte("omit.txt\n"), 0o600),
	)
	require.NoError(
		t,
		os.Mkdir(filepath.Join(dir, devcontainerconfig.DevsyContextFeatureFolder), 0o700),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, devcontainerconfig.DevsyContextFeatureFolder, "retained.txt"),
			[]byte("user-owned"),
			0o600,
		),
	)

	return dir
}

func TestNewLocalTunnelClient_ExcludesPersistedBuildResidue(t *testing.T) {
	t.Setenv("BUILD_CONTEXT", "wrong-current-context")
	for _, contextPath := range []string{".", ".devcontainer"} {
		t.Run(contextPath, func(t *testing.T) {
			root := t.TempDir()
			generated := filepath.Join(
				contextPath,
				devcontainerconfig.DevsyContextFeatureFolder,
				"0",
				"devcontainer-features.env",
			)
			unrelated := filepath.Join(
				"private",
				devcontainerconfig.DevsyContextFeatureFolder,
				"retained.txt",
			)
			for _, file := range []string{generated, unrelated} {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0o700))
				require.NoError(
					t,
					os.WriteFile(filepath.Join(root, file), []byte("fixture-secret"), 0o600),
				)
			}
			persisted := persistedSnapshotResult(t, root, contextPath)
			buildContext, err := devcontainerconfig.SnapshotBuildContext(persisted)
			require.NoError(t, err)
			require.Equal(t, filepath.Join(root, contextPath), buildContext)
			mount := &devcontainerconfig.Mount{
				Type:   testBindMountType,
				Source: root,
				Target: "/workspace",
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, cleanup, err := newLocalTunnelClient(
				ctx,
				[]*devcontainerconfig.Mount{mount},
				mount,
				buildContext,
			)
			require.NoError(t, err)
			defer cleanup()
			stream, err := client.StreamSnapshotVolumes(ctx, &tunnel.Empty{})
			require.NoError(t, err)
			names := snapshotArchiveNames(t, stream)
			require.NotContains(t, names, "workspace/"+filepath.ToSlash(generated))
			require.Contains(t, names, "workspace/"+filepath.ToSlash(unrelated))
		})
	}
}

func TestSnapshotBuildContext_RequiresMetadata(t *testing.T) {
	const invalidMetadata = "invalid local build context metadata"
	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		path    string
		context string
		want    string
	}{
		{name: "missing", want: invalidMetadata},
		{name: "escaping", path: "../devcontainer.json", want: invalidMetadata},
		{name: "absolute", path: filepath.Join(root, "devcontainer.json"), want: invalidMetadata},
		{name: "unresolved", path: "devcontainer.json", context: "${localEnv:MISSING}", want: "unresolved build context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &devcontainerconfig.Result{
				DevContainerConfigWithPath: &devcontainerconfig.DevContainerConfigWithPath{
					Path: tc.path,
					Config: &devcontainerconfig.DevContainerConfig{
						DockerfileContainer: devcontainerconfig.DockerfileContainer{
							Build: &devcontainerconfig.ConfigBuildOptions{Context: tc.context},
						},
					},
				},
				SubstitutionContext: &devcontainerconfig.SubstitutionContext{
					LocalWorkspaceFolder: root,
				},
			}
			_, err := devcontainerconfig.SnapshotBuildContext(result)
			require.ErrorContains(t, err, tc.want)
		})
	}
	_, err := devcontainerconfig.SnapshotBuildContext(&devcontainerconfig.Result{})
	require.ErrorContains(t, err, "missing build context metadata")
}

func TestSnapshotBuildContext_LiveExternalOrigin(t *testing.T) {
	root := t.TempDir()
	result := &devcontainerconfig.Result{
		DevContainerConfigWithPath: &devcontainerconfig.DevContainerConfigWithPath{
			Path: "../external/devcontainer.json",
			Config: &devcontainerconfig.DevContainerConfig{
				Origin: filepath.Join(root, "devcontainer.json"),
			},
		},
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{},
	}
	buildContext, err := devcontainerconfig.SnapshotBuildContext(result)
	require.NoError(t, err)
	require.Equal(t, root, buildContext)
}

func persistedSnapshotResult(t *testing.T, root, contextPath string) *devcontainerconfig.Result {
	t.Helper()
	result := &devcontainerconfig.Result{
		DevContainerConfigWithPath: &devcontainerconfig.DevContainerConfigWithPath{
			Path: "devcontainer.json",
			Config: &devcontainerconfig.DevContainerConfig{
				DockerfileContainer: devcontainerconfig.DockerfileContainer{
					Build: &devcontainerconfig.ConfigBuildOptions{
						Context: "${localEnv:BUILD_CONTEXT}",
					},
				},
			},
		},
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			LocalWorkspaceFolder: root,
			Env:                  map[string]string{"BUILD_CONTEXT": contextPath},
		},
	}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	var persisted devcontainerconfig.Result
	require.NoError(t, json.Unmarshal(data, &persisted))
	require.Empty(t, persisted.GeneratedBuildArtifacts)
	return &persisted
}

func snapshotArchiveNames(t *testing.T, stream tunnel.Tunnel_StreamSnapshotVolumesClient) []string {
	t.Helper()
	var archive bytes.Buffer
	for {
		chunk, err := stream.Recv()
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		archive.Write(chunk.Content)
	}
	reader := tar.NewReader(&archive)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return names
		}
		require.NoError(t, err)
		names = append(names, header.Name)
	}
}

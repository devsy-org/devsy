package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
)

type CreateCmd struct {
	*flags.GlobalFlags

	Registry string
	Message  string
}

func NewCreateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CreateCmd{GlobalFlags: globalFlags}
	createCmd := &cobra.Command{
		Use:   "create [flags] [workspace-path|workspace-name]",
		Short: "Snapshot a workspace's container filesystem and volumes",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(cobraCmd.Context(), devsyConfig, args)
		},
	}
	cliflags.Add(
		createCmd,
		cliflags.String(
			&cmd.Registry,
			"registry",
			"",
			"Registry to push the snapshot to, overriding SNAPSHOT_REGISTRY",
		),
		cliflags.String(&cmd.Message, "message", "", "Optional message describing this snapshot"),
	)
	return createCmd
}

// snapshotTarget is the workspace and registry snapshot create resolved and
// validated before any registry or driver work begins.
type snapshotTarget struct {
	Client     clientpkg.WorkspaceClient
	Workspace  *provider.Workspace
	Repository string
}

// The manifest is pushed last so a snapshot only becomes visible once every
// blob it references already exists in the registry.
func (cmd *CreateCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	target, err := cmd.resolveTarget(ctx, devsyConfig, args)
	if err != nil {
		return err
	}

	ref, err := snapshotpkg.NewRef(target.Repository, target.Workspace.ID, time.Now())
	if err != nil {
		return err
	}

	fsTag := ref.FSImageRef()
	if err := snapshotpkg.CheckPushPermissions(ctx, fsTag); err != nil {
		return fmt.Errorf("check push permissions for %s: %w", fsTag, err)
	}

	img, err := cmd.commitAndPushImage(ctx, target, fsTag)
	if err != nil {
		return err
	}

	vols, err := cmd.pushVolumes(ctx, target.Workspace, target.Repository)
	if err != nil {
		return err
	}

	manifest, err := snapshotpkg.BuildManifest(snapshotpkg.BuildManifestOptions{
		WorkspaceUID:            target.Workspace.UID,
		CreatedAt:               time.Now(),
		DevContainerHash:        devContainerConfigHash(target.Workspace.DevContainerConfig),
		SourceProvider:          target.Workspace.Provider.Name,
		Message:                 cmd.Message,
		MountPrefix:             vols.MountPrefix,
		RunArgs:                 vols.RunArgs,
		ContainerEnv:            vols.ContainerEnv,
		ContainerImageMediaType: img.MediaType,
		ContainerImageDigest:    img.Digest,
		ContainerImageSize:      img.Size,
		VolumesDigest:           vols.Digest,
		VolumesSize:             vols.Size,
	})
	if err != nil {
		return err
	}

	if err := snapshotpkg.PushManifest(ctx, ref.String(), manifest); err != nil {
		return fmt.Errorf("push snapshot manifest: %w", err)
	}

	log.Infof("created snapshot: ref=%s", ref.String())
	//nolint:forbidigo // CLI stdout output, scriptable via `ref=$(devsy snapshot create ...)`
	fmt.Println(ref.String())
	return nil
}

// resolveTarget resolves the target workspace and validates it can be
// snapshotted (local, non-proxy) before any registry or driver work begins.
func (cmd *CreateCmd) resolveTarget(
	ctx context.Context, devsyConfig *config.Config, args []string,
) (*snapshotTarget, error) {
	baseClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return nil, err
	}

	wsClient, ok := baseClient.(clientpkg.WorkspaceClient)
	if !ok {
		return nil, fmt.Errorf("this command is not supported for proxy providers")
	}
	workspaceConfig := wsClient.WorkspaceConfig()

	if err := checkLocalWorkspace(workspaceConfig); err != nil {
		return nil, err
	}

	repository, err := resolveRegistry(cmd.Registry, devsyConfig)
	if err != nil {
		return nil, err
	}

	return &snapshotTarget{
		Client:     wsClient,
		Workspace:  workspaceConfig,
		Repository: repository,
	}, nil
}

// pushedImage describes a successfully committed and pushed container image.
type pushedImage struct {
	Digest    string
	Size      int64
	MediaType string
}

// commitAndPushImage commits the target workspace's running container
// filesystem and pushes it as fsTag, returning the pushed image's digest,
// size, and the media type the registry actually reported for it.
func (cmd *CreateCmd) commitAndPushImage(
	ctx context.Context, target *snapshotTarget, fsTag string,
) (*pushedImage, error) {
	imgDriver, err := cmd.imageDriver(ctx, target.Client)
	if err != nil {
		return nil, err
	}

	runnerID := devcontainer.GetRunnerIDFromWorkspace(target.Workspace)
	if err := imgDriver.CommitContainer(ctx, runnerID, fsTag); err != nil {
		return nil, fmt.Errorf("commit container: %w", err)
	}
	if err := imgDriver.PushDevContainer(ctx, fsTag); err != nil {
		return nil, fmt.Errorf("push container image: %w", err)
	}

	img, err := pushedImageDigestAndSize(ctx, target.Repository, fsTag)
	if err != nil {
		return nil, fmt.Errorf("read pushed image digest: %w", err)
	}
	return img, nil
}

// imageDriver and the volumes tunnel (newLocalTunnelClient) both assume the
// CLI process is co-located with the workspace's Docker daemon and bind mount
// sources; a machine-provider workspace's container runs on a remote host, so
// snapshotting it would silently operate on the wrong (local) filesystem.
func checkLocalWorkspace(workspaceConfig *provider.Workspace) error {
	if workspaceConfig.Machine.ID != "" {
		return fmt.Errorf(
			"snapshot create currently supports only local workspaces; "+
				"workspace %s runs on a machine provider",
			workspaceConfig.ID,
		)
	}
	return nil
}

// checkSingleMount requires exactly one mount: RestoreVolumes
// (pkg/agent/snapshot/restore.go) only supports a single mount, since the
// combined volumes tar has no way to disambiguate which entries belong to
// which mount when there is more than one, and the manifest's MountPrefix
// is derived from that one mount's target. Without this check, create would
// either push a snapshot restore can't disambiguate, or panic indexing
// mounts[0] on a workspace with no bind mounts at all.
func checkSingleMount(mounts []*devcontainerconfig.Mount) error {
	if len(mounts) == 0 {
		return fmt.Errorf("snapshot create requires a workspace mount; none found")
	}
	if len(mounts) > 1 {
		return fmt.Errorf(
			"snapshot create does not yet support multiple mounts (%d found); "+
				"restore cannot disambiguate entries across mounts",
			len(mounts),
		)
	}
	return nil
}

// snapshotDriver bundles the two capabilities snapshot create needs: pushing
// the committed image (ImageDriver) and committing the container filesystem
// (SnapshotCapableDriver). Drivers that delegate to an external orchestrator
// (Kubernetes, custom drivers) or that can't commit a container filesystem
// (Apple's `container`) are rejected here via the type assertions.
type snapshotDriver struct {
	driver.ImageDriver
	driver.SnapshotCapableDriver
}

func (cmd *CreateCmd) imageDriver(
	ctx context.Context, wsClient clientpkg.WorkspaceClient,
) (*snapshotDriver, error) {
	_, workspaceInfo, err := wsClient.AgentInfo(provider.CLIOptions{})
	if err != nil {
		return nil, fmt.Errorf("read workspace agent info: %w", err)
	}

	d, err := drivercreate.NewDriver(ctx, workspaceInfo)
	if err != nil {
		return nil, fmt.Errorf("create driver: %w", err)
	}

	imgDriver, ok := d.(driver.ImageDriver)
	if !ok {
		return nil, fmt.Errorf(
			"provider %s cannot create snapshots (no image driver support)",
			workspaceInfo.Agent.Driver,
		)
	}
	snapshotCapable, ok := d.(driver.SnapshotCapableDriver)
	if !ok {
		return nil, fmt.Errorf(
			"provider %s cannot create snapshots (no container commit support)",
			workspaceInfo.Agent.Driver,
		)
	}
	return &snapshotDriver{ImageDriver: imgDriver, SnapshotCapableDriver: snapshotCapable}, nil
}

// pushedVolumes describes a successfully-pushed volumes blob.
type pushedVolumes struct {
	Digest       string
	Size         int64
	MountPrefix  string
	RunArgs      []string
	ContainerEnv map[string]string
}

// The volumes RPC (StreamSnapshotVolumes) is served by a tunnelServer reading
// directly off local disk, so this only works when the CLI process is
// co-located with those mount source paths — true for the docker driver's
// local bind mounts, the only driver CommitContainer currently supports.
func (cmd *CreateCmd) pushVolumes(
	ctx context.Context, workspaceConfig *provider.Workspace, repository string,
) (*pushedVolumes, error) {
	result, err := provider.LoadWorkspaceResult(workspaceConfig.Context, workspaceConfig.ID)
	if err != nil {
		return nil, fmt.Errorf("load workspace result: %w", err)
	}
	if result == nil || result.SubstitutionContext == nil || result.MergedConfig == nil {
		return nil, fmt.Errorf(
			"workspace result is missing mount information; run `devsy up` first",
		)
	}
	mounts := devcontainerconfig.GetMounts(result)
	if err := checkSingleMount(mounts); err != nil {
		return nil, err
	}
	mountPrefix := strings.TrimPrefix(mounts[0].Target, "/")

	tunnelClient, cleanup, err := newLocalTunnelClient(ctx, mounts)
	if err != nil {
		return nil, fmt.Errorf("create local snapshot tunnel: %w", err)
	}
	defer cleanup()

	digest, size, err := snapshotpkg.PushVolumesFromTunnel(ctx, tunnelClient, repository)
	if err != nil {
		return nil, fmt.Errorf("push volumes: %w", err)
	}
	return &pushedVolumes{
		Digest:       digest,
		Size:         size,
		MountPrefix:  mountPrefix,
		RunArgs:      result.MergedConfig.RunArgs,
		ContainerEnv: redactedContainerEnv(result.MergedConfig.ContainerEnv),
	}, nil
}

// redactedContainerEnv drops entries that carry runtime secrets rather than
// plain devcontainer.json settings, so replaying them on restore (via the
// manifest's sh.devsy.snapshot.container-env annotation) can't leak a
// credential into a shared registry. EnvWorkspaceDaemonConfig in particular
// is injected by injectDaemonEntrypoint (pkg/devcontainer/single.go) and
// carries the platform access key for platform-managed workspaces; the
// restored container gets its own copy of this at container-start time
// regardless, so dropping it here does not change restore's behavior.
func redactedContainerEnv(env map[string]string) map[string]string {
	if _, ok := env[config.EnvWorkspaceDaemonConfig]; !ok {
		return env
	}
	redacted := make(map[string]string, len(env)-1)
	for k, v := range env {
		if k == config.EnvWorkspaceDaemonConfig {
			continue
		}
		redacted[k] = v
	}
	return redacted
}

// newLocalTunnelClient wires a tunnelServer (serving StreamSnapshotVolumes
// off mounts) directly to a tunnel.TunnelClient over an in-process pipe pair,
// reusing the same gRPC tunnel machinery used for `up`/`ssh --start-services`
// without an actual SSH hop. The returned cleanup func must be called once
// the client is no longer needed.
func newLocalTunnelClient(
	ctx context.Context, mounts []*devcontainerconfig.Mount,
) (tunnel.TunnelClient, func(), error) {
	serverCtx, cancel := context.WithCancel(ctx)

	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	tunnelServ := tunnelserver.New(tunnelserver.WithMounts(mounts))
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tunnelServ.Run(serverCtx, clientToServerR, serverToClientW)
	}()

	tunnelClient, err := tunnelserver.NewTunnelClient(serverToClientR, clientToServerW, false, 0)
	if err != nil {
		cancel()
		_ = clientToServerW.Close()
		_ = serverToClientW.Close()
		<-serverDone
		return nil, nil, fmt.Errorf("dial local snapshot tunnel: %w", err)
	}

	cleanup := func() {
		cancel()
		_ = clientToServerW.Close()
		_ = serverToClientW.Close()
		<-serverDone
	}
	return tunnelClient, cleanup, nil
}

// pushedImageDigestAndSize reads back the digest and size of the manifest
// just pushed to imageRef, so the snapshot manifest can reference the
// container image layer by digest.
//
// It also re-pushes the same manifest bytes as a plain blob into repository:
// registries store manifests and blobs in separate content stores, and the
// snapshot manifest built in Run references this digest as one of its
// layers[] — a manifest-push validates every layer digest exists as a blob,
// which fails with MANIFEST_BLOB_UNKNOWN without this re-push.
func pushedImageDigestAndSize(
	ctx context.Context,
	repository, imageRef string,
) (*pushedImage, error) {
	ref, err := snapshotpkg.ParseImageReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}

	keychain, err := image.GetKeychain(ctx)
	if err != nil {
		return nil, fmt.Errorf("create authentication keychain: %w", err)
	}

	desc, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(keychain))
	if err != nil {
		return nil, fmt.Errorf(
			"get image descriptor for %s: %w",
			imageRef,
			image.SanitizeRegistryError(err),
		)
	}

	if _, _, err := snapshotpkg.PushBlob(
		ctx,
		repository,
		string(desc.MediaType),
		bytes.NewReader(desc.Manifest),
	); err != nil {
		return nil, fmt.Errorf("push image manifest %s as blob: %w", desc.Digest, err)
	}

	return &pushedImage{
		Digest:    desc.Digest.String(),
		Size:      desc.Size,
		MediaType: string(desc.MediaType),
	}, nil
}

// devContainerConfigHash lets restores detect drift between the snapshot and
// the project's current devcontainer.json.
func devContainerConfigHash(cfg *devcontainerconfig.DevContainerConfig) string {
	if cfg == nil {
		return ""
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return hash.String(string(raw))
}

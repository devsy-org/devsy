//go:build !windows

package agentcontainer

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	agentsnapshot "github.com/devsy-org/devsy/pkg/agent/snapshot"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/ts"
)

func (cmd *SetupContainerCmd) syncMounts(ctx context.Context, state *containerState) error {
	mounts := config.GetMounts(state.setupInfo)
	if state.workspaceInfo.Source.Snapshot != "" {
		return restoreSnapshotMounts(ctx, state, mounts)
	}

	if !cmd.StreamMounts {
		return nil
	}

	log.Debugf("syncing mounts: %v", mounts)
	for _, m := range mounts {
		if !state.workspaceInfo.CLIOptions.Reset {
			files, err := os.ReadDir(m.Target)
			if err == nil && len(files) > 0 {
				log.Debugf("skip stream mount %s because it is not empty", m.Target)
				continue
			}
		}

		if err := streamMount(
			ctx,
			state.workspaceInfo,
			m,
			state.tunnelClient,
		); err != nil {
			return err
		}
	}

	return nil
}

// restoreSnapshotMounts restores a snapshot-sourced workspace's volumes,
// skipping the restore if the sole mount target already has real content.
func restoreSnapshotMounts(
	ctx context.Context,
	state *containerState,
	mounts []*config.Mount,
) error {
	if !state.workspaceInfo.CLIOptions.Reset && len(mounts) == 1 &&
		skipSnapshotRestore(mounts[0].Target) {
		return nil
	}
	log.Infof("restoring snapshot volumes from %s", state.workspaceInfo.Source.Snapshot)
	if err := agentsnapshot.RestoreVolumes(
		ctx,
		state.workspaceInfo.Source.Snapshot,
		mounts,
		state.workspaceInfo.CLIOptions.Reset,
	); err != nil {
		return fmt.Errorf("restore snapshot volumes: %w", err)
	}
	return nil
}

// synthesizedDevContainerName is the devcontainer.json devsy synthesizes for
// image/none-sourced workspaces (pkg/devcontainer's saveSynthesizedConfig). A
// snapshot-sourced restore gets one too, written into the mount target before
// syncMounts ever runs, so it must not count as "already has content" below.
var synthesizedDevContainerName = ".devcontainer." + config2.BinaryName + ".json"

// skipSnapshotRestore reports whether target already has real content, in
// which case a snapshot restore into it would be destructive and is skipped.
func skipSnapshotRestore(target string) bool {
	files, err := os.ReadDir(target)
	if err != nil {
		return false
	}
	var names []string
	for _, f := range files {
		if f.Name() == synthesizedDevContainerName {
			continue
		}
		names = append(names, f.Name())
	}
	if len(names) == 0 {
		return false
	}
	log.Debugf("skip snapshot restore because %s is not empty: entries=%v", target, names)
	return true
}

func streamMount(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
	tunnelClient tunnel.TunnelClient,
) error {
	// if we have a platform workspace socket we connect directly to it
	if workspaceInfo.CLIOptions.Platform.Enabled {
		return streamMountFromPlatform(ctx, workspaceInfo, m)
	}

	return streamMountFromTunnel(ctx, m, tunnelClient)
}

func streamMountFromPlatform(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
) error {
	log.Infof("Download %s into DevContainer %s", m.Source, m.Target)
	req, err := buildPlatformDownloadRequest(ctx, workspaceInfo, m)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				//nolint:gosec // pre-existing, relocated by funlen extraction; out of scope for this PR
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download workspace: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// check if the response is ok
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"download workspace: body = %s, status = %s",
			string(body),
			resp.Status,
		)
	}

	progressReader := &progressReader{
		Reader: resp.Body,
	}

	if err := extract.Extract(progressReader, m.Target); err != nil {
		return fmt.Errorf("stream mount %s: %w", m.String(), err)
	}

	return nil
}

// buildPlatformDownloadRequest builds the authenticated request to the
// runner proxy socket that serves m.Source for download.
func buildPlatformDownloadRequest(
	ctx context.Context,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	m *config.Mount,
) (*http.Request, error) {
	downloadURL := fmt.Sprintf(
		"https://%s/kubernetes/management/apis/management.devsy.sh/v1/namespaces/%s/devsyworkspaceinstances/%s/download?path=%s", //nolint:lll // pre-existing, relocated by funlen extraction; out of scope for this PR
		ts.RemoveProtocol(workspaceInfo.CLIOptions.Platform.PlatformHost),
		workspaceInfo.CLIOptions.Platform.InstanceNamespace,
		workspaceInfo.CLIOptions.Platform.InstanceName,
		url.QueryEscape(m.Source),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set(
		"Authorization",
		fmt.Sprintf("Bearer %s", workspaceInfo.CLIOptions.Platform.AccessKey),
	)

	return req, nil
}

func streamMountFromTunnel(
	ctx context.Context,
	m *config.Mount,
	tunnelClient tunnel.TunnelClient,
) error {
	log.Infof("Copy %s into DevContainer %s", m.Source, m.Target)
	stream, err := tunnelClient.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: m.String()})
	if err != nil {
		return fmt.Errorf("init stream mount %s: %w", m.String(), err)
	}

	if err := extract.Extract(tunnelserver.NewStreamReader(stream), m.Target); err != nil {
		return fmt.Errorf("stream mount %s: %w", m.String(), err)
	}

	return nil
}

type progressReader struct {
	Reader io.Reader

	lastMessage time.Time
	bytesRead   int64
}

func (p *progressReader) Read(b []byte) (n int, err error) {
	n, err = p.Reader.Read(b)
	p.bytesRead += int64(n)
	if time.Since(p.lastMessage) > time.Second*4 {
		log.Infof("downloaded %.2f MB", float64(p.bytesRead)/1024/1024)
		p.lastMessage = time.Now()
	}

	return n, err
}

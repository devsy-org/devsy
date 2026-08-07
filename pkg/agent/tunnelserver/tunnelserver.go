package tunnelserver

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devsyconfig"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/gitsshsigning"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/netstat"
	"github.com/devsy-org/devsy/pkg/platform"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/stdio"
	"github.com/moby/patternmatcher/ignorefile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func RunServicesServer(
	ctx context.Context,
	reader io.Reader,
	writer io.WriteCloser,
	allowGitCredentials, allowDockerCredentials bool,
	forwarder netstat.Forwarder,
	workspace *provider2.Workspace,
	options ...Option,
) error {
	options = append(options,
		WithForwarder(forwarder),
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
		WithWorkspace(workspace),
	)
	tunnelServ := New(options...)

	return tunnelServ.Run(ctx, reader, writer)
}

func RunUpServer(
	ctx context.Context,
	reader io.Reader,
	writer io.WriteCloser,
	allowGitCredentials, allowDockerCredentials bool,
	workspace *provider2.Workspace,
	options ...Option,
) (*config.Result, error) {
	options = append(options,
		WithWorkspace(workspace),
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
	)
	tunnelServ := New(options...)

	return tunnelServ.RunWithResult(ctx, reader, writer)
}

func RunSetupServer(
	ctx context.Context,
	reader io.Reader,
	writer io.WriteCloser,
	allowGitCredentials, allowDockerCredentials bool,
	mounts []*config.Mount,
	options ...Option,
) (*config.Result, error) {
	options = append(options,
		WithMounts(mounts),
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
		WithAllowKubeConfig(true),
	)
	tunnelServ := New(options...)
	tunnelServ.allowPlatformOptions = true

	return tunnelServ.RunWithResult(ctx, reader, writer)
}

func New(options ...Option) *tunnelServer {
	s := &tunnelServer{statusReporter: status.Nop()}
	for _, o := range options {
		s = o(s)
	}

	return s
}

type tunnelServer struct {
	tunnel.UnimplementedTunnelServer

	// stream mounts
	mounts []*config.Mount

	forwarder              netstat.Forwarder
	allowGitCredentials    bool
	allowDockerCredentials bool
	allowKubeConfig        bool
	allowPlatformOptions   bool
	resultMu               sync.Mutex
	result                 *config.Result
	workspace              *provider2.Workspace

	platformOptions *devsy.PlatformOptions
	secrets         []*tunnel.Secret
	gitToken        *provider2.GitToken

	statusReporter status.Reporter
}

func (t *tunnelServer) RunWithResult(
	ctx context.Context,
	reader io.Reader,
	writer io.WriteCloser,
) (*config.Result, error) {
	lis := stdio.NewStdioListener(reader, writer, false)
	s := grpc.NewServer()
	tunnel.RegisterTunnelServer(s, t)
	reflection.Register(s)
	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Serve(lis)
	}()

	stopCtx, stopCancel := context.WithCancel(ctx)
	defer stopCancel()
	go func() {
		<-stopCtx.Done()
		s.Stop()
	}()

	select {
	case err := <-errChan:
		if result := t.getResult(); result != nil {
			return result, nil
		}
		return nil, err
	case <-ctx.Done():
		if result := t.getResult(); result != nil {
			return result, nil
		}
		return nil, ctx.Err()
	}
}

func (t *tunnelServer) Run(ctx context.Context, reader io.Reader, writer io.WriteCloser) error {
	_, err := t.RunWithResult(ctx, reader, writer)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func (t *tunnelServer) ForwardPort(
	ctx context.Context,
	portRequest *tunnel.ForwardPortRequest,
) (*tunnel.ForwardPortResponse, error) {
	if t.forwarder == nil {
		return nil, fmt.Errorf("cannot forward ports")
	}

	err := t.forwarder.Forward(portRequest.Port, netstat.PortForwardAttribute{})
	if err != nil {
		return nil, fmt.Errorf("error forwarding port %s: %w", portRequest.Port, err)
	}

	return &tunnel.ForwardPortResponse{}, nil
}

func (t *tunnelServer) StopForwardPort(
	ctx context.Context,
	portRequest *tunnel.StopForwardPortRequest,
) (*tunnel.StopForwardPortResponse, error) {
	if t.forwarder == nil {
		return nil, fmt.Errorf("cannot forward ports")
	}

	err := t.forwarder.StopForward(portRequest.Port)
	if err != nil {
		return nil, fmt.Errorf("error stop forwarding port %s: %w", portRequest.Port, err)
	}

	return &tunnel.StopForwardPortResponse{}, nil
}

func (t *tunnelServer) DockerCredentials(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	if !t.allowDockerCredentials {
		return nil, fmt.Errorf("docker credentials forbidden")
	}

	request := &dockercredentials.Request{}
	err := json.Unmarshal([]byte(message.Message), request)
	if err != nil {
		return nil, err
	}

	// check if list or get
	if request.ServerURL != "" {
		credentials, err := dockercredentials.GetAuthConfig(request.ServerURL)
		if err != nil {
			return nil, err
		}

		out, err := json.Marshal(credentials)
		if err != nil {
			return nil, err
		}

		return &tunnel.Message{Message: string(out)}, nil
	}

	// do a list
	listResponse, err := dockercredentials.ListCredentials()
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(listResponse)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) Secrets(
	_ context.Context,
	_ *tunnel.Empty,
) (*tunnel.SecretsResponse, error) {
	return &tunnel.SecretsResponse{Secrets: t.secrets}, nil
}

func (t *tunnelServer) GitUser(ctx context.Context, empty *tunnel.Empty) (*tunnel.Message, error) {
	workingDir := ""
	if t.workspace != nil {
		workingDir = t.workspace.Source.LocalFolder
	}
	gitUser, err := gitcredentials.GetUser(ctx, "", workingDir)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(gitUser)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{
		Message: string(out),
	}, nil
}

func (t *tunnelServer) GitCredentials(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	log.Debugf(
		"getting git credentials: allowGitCredentials=%v, workspaceIsNil=%v",
		t.allowGitCredentials,
		t.workspace == nil,
	)
	if !t.allowGitCredentials {
		return nil, fmt.Errorf("git credentials forbidden")
	}

	credentials := &gitcredentials.GitCredentials{}
	err := json.Unmarshal([]byte(message.Message), credentials)
	if err != nil {
		return nil, fmt.Errorf("decode git credentials request: %w", err)
	}

	if msg, ok, err := t.gitTokenCredentials(credentials); ok || err != nil {
		return msg, err
	}

	credentials, err = t.populateGitCredentials(ctx, credentials)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func applyPlatformGitCredentials(
	credentials *gitcredentials.GitCredentials,
	platformOptions *devsy.PlatformOptions,
) {
	gitHttpCredentials := slices.Concat(
		platformOptions.UserCredentials.GitHttp,
		platformOptions.ProjectCredentials.GitHttp)
	if len(gitHttpCredentials) == 0 {
		return
	}
	if len(gitHttpCredentials) == 1 {
		credentials.Username = gitHttpCredentials[0].User
		credentials.Password = gitHttpCredentials[0].Password
		credentials.Path = gitHttpCredentials[0].Path
		return
	}
	for _, credential := range gitHttpCredentials {
		if credential.Host == credentials.Host {
			credentials.Username = credential.User
			credentials.Password = credential.Password
			credentials.Path = credential.Path
			break
		}
	}
}

func (t *tunnelServer) GitSSHSignature(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	signatureRequest := &gitsshsigning.GitSSHSignatureRequest{}
	err := json.Unmarshal([]byte(message.Message), signatureRequest)
	if err != nil {
		return nil, fmt.Errorf("git ssh sign request: %w", err)
	}

	signatureResponse, err := signatureRequest.Sign()
	if err != nil {
		return nil, fmt.Errorf("get git ssh signature: %w", err)
	}

	out, err := json.Marshal(signatureResponse)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) DevsyConfig(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	if t.workspace == nil {
		return nil, fmt.Errorf("devsy platform config request: no workspace bound to tunnel")
	}

	response, err := devsyconfig.Read(t.workspace)
	if err != nil {
		return nil, fmt.Errorf("read devsy platform config: %w", err)
	}

	out, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) KubeConfig(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	if !t.allowKubeConfig {
		return nil, fmt.Errorf("kube config forbidden")
	}

	kubeConfig, err := platform.NewInstanceKubeConfig(ctx, t.platformOptions)
	if err != nil {
		return nil, fmt.Errorf("create kube config: %w", err)
	}

	return &tunnel.Message{Message: string(kubeConfig)}, nil
}

func (t *tunnelServer) GPGPublicKeys(
	ctx context.Context,
	message *tunnel.Message,
) (*tunnel.Message, error) {
	rawPubKeys, err := gpg.GetHostPubKey()
	if err != nil {
		return nil, fmt.Errorf("get gpg host public keys: %w", err)
	}

	pubKeyArgument := base64.StdEncoding.EncodeToString(rawPubKeys)

	return &tunnel.Message{Message: pubKeyArgument}, nil
}

func (t *tunnelServer) SendResult(
	ctx context.Context,
	result *tunnel.Message,
) (*tunnel.Empty, error) {
	parsedResult := &config.Result{}
	err := json.Unmarshal([]byte(result.Message), parsedResult)
	if err != nil {
		return nil, err
	}

	t.setResult(parsedResult)
	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) Ping(context.Context, *tunnel.Empty) (*tunnel.Empty, error) {
	log.Debug("received ping from agent")
	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) Log(ctx context.Context, message *tunnel.LogMessage) (*tunnel.Empty, error) {
	switch message.LogLevel {
	case tunnel.LogLevel_DEBUG:
		log.Debug(strings.TrimSpace(message.Message))
	case tunnel.LogLevel_INFO:
		log.Info(strings.TrimSpace(message.Message))
	case tunnel.LogLevel_WARNING:
		log.Warn(strings.TrimSpace(message.Message))
	case tunnel.LogLevel_ERROR:
		log.Error(strings.TrimSpace(message.Message))
	case tunnel.LogLevel_DONE:
		log.Info(strings.TrimSpace(message.Message))
	}

	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) Status(
	ctx context.Context,
	update *tunnel.StatusUpdate,
) (*tunnel.Empty, error) {
	// No Pipeline: the tunnel only carries container-side up events, and the
	// host's reporter stamps the pipeline on arrival.
	t.statusReporter.Report(status.Event{
		Phase:   status.Phase(update.Phase),
		Step:    update.Step,
		Started: update.Started,
		Err:     update.Error,
	})
	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) StreamWorkspace(
	message *tunnel.Empty,
	stream tunnel.Tunnel_StreamWorkspaceServer,
) error {
	if t.platformOptions != nil && t.platformOptions.Enabled && !t.allowPlatformOptions {
		return fmt.Errorf(
			"streaming workspace from local computer to platform workspace is not supported. " +
				"Specify a Git repository to clone instead",
		)
	}
	if t.workspace == nil {
		return fmt.Errorf("workspace is nil")
	}

	// Get .devsyignore files to exclude
	excludes := []string{}
	f, err := os.Open(filepath.Join(t.workspace.Source.LocalFolder, pkgconfig.IgnoreFileName))
	if err == nil {
		excludes, err = ignorefile.ReadAll(f)
		if err != nil {
			log.Warnf("error reading %s file: error=%v", pkgconfig.IgnoreFileName, err)
		}
	}

	excludes = append(excludes, config.BuildArtifactExcludes()...)

	buf := bufio.NewWriterSize(NewStreamWriter(stream), 10*1024)
	err = extract.WriteTarExclude(buf, t.workspace.Source.LocalFolder, false, excludes)
	if err != nil {
		return err
	}

	// make sure buffer is flushed
	return buf.Flush()
}

func (t *tunnelServer) StreamMount(
	message *tunnel.StreamMountRequest,
	stream tunnel.Tunnel_StreamMountServer,
) error {
	if t.platformStreamBlocked() {
		return fmt.Errorf(
			"streaming mounts from local computer to platform workspace is not supported. " +
				"Specify a Git repository to clone instead",
		)
	}

	var mount *config.Mount
	for _, m := range t.mounts {
		if m.String() == message.Mount {
			mount = m
			break
		}
	}
	if mount == nil {
		return fmt.Errorf("mount %s is not allowed to download", message.Mount)
	}

	excludes := append(t.workspaceIgnoreExcludes(), config.BuildArtifactExcludes()...)

	buf := bufio.NewWriterSize(NewStreamWriter(stream), 10*1024)
	err := extract.WriteTarExclude(buf, mount.Source, false, excludes)
	if err != nil {
		return err
	}

	// make sure buffer is flushed
	return buf.Flush()
}

func (t *tunnelServer) StreamSnapshotVolumes(
	message *tunnel.Empty,
	stream tunnel.Tunnel_StreamSnapshotVolumesServer,
) error {
	if t.platformStreamBlocked() {
		return fmt.Errorf(
			"streaming snapshot volumes from a platform workspace is not supported",
		)
	}

	buf := bufio.NewWriterSize(NewStreamWriter(stream), 10*1024)
	tw := tar.NewWriter(buf)

	excludes := append(t.workspaceIgnoreExcludes(), config.BuildArtifactExcludes()...)
	for _, m := range t.mounts {
		prefix := strings.TrimPrefix(m.Target, "/")
		if err := appendDirToTar(tw, m.Source, prefix, excludes); err != nil {
			return fmt.Errorf("tar mount %s: %w", m.Target, err)
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close snapshot volumes tar: %w", err)
	}
	return buf.Flush()
}

// appendDirToTar writes localDir's contents under prefix inside an
// already-open tar.Writer, reusing extract.WriteTarExclude's on-disk walk by
// tarring into a pipe and re-prefixing entries; kept simple since snapshot
// volume archives combine multiple mount roots into one stream.
func appendDirToTar(tw *tar.Writer, localDir, prefix string, excludes []string) error {
	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()

	errCh := make(chan error, 1)
	go func() {
		err := extract.WriteTarExclude(pw, localDir, false, excludes)
		errCh <- err
		_ = pw.CloseWithError(err)
	}()

	tr := tar.NewReader(pr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		//nolint:gosec // re-prefixing our own just-built tar, not an untrusted archive
		hdr.Name = path.Join(prefix, hdr.Name)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil { //nolint:gosec // bounded by source tar entries
			return err
		}
	}
	return <-errCh
}

func (t *tunnelServer) gitTokenCredentials(
	credentials *gitcredentials.GitCredentials,
) (*tunnel.Message, bool, error) {
	// A configured git token is returned only for its own host, so it is never
	// offered to other hosts git contacts (submodules, redirects).
	if t.gitToken == nil || t.gitToken.Token == "" ||
		!strings.EqualFold(credentials.Host, t.gitToken.Host) {
		return nil, false, nil
	}

	credentials.Username = t.gitToken.Username
	credentials.Password = t.gitToken.Token
	// #nosec G117 -- git credential response legitimately carries the token.
	out, err := json.Marshal(credentials)
	if err != nil {
		return nil, false, err
	}
	return &tunnel.Message{Message: string(out)}, true, nil
}

func (t *tunnelServer) populateGitCredentials(
	ctx context.Context,
	credentials *gitcredentials.GitCredentials,
) (*gitcredentials.GitCredentials, error) {
	if t.platformOptions != nil && t.platformOptions.Enabled {
		applyPlatformGitCredentials(credentials, t.platformOptions)
		return credentials, nil
	}
	return t.resolveHostGitCredentials(ctx, credentials)
}

func (t *tunnelServer) resolveHostGitCredentials(
	ctx context.Context,
	credentials *gitcredentials.GitCredentials,
) (*gitcredentials.GitCredentials, error) {
	if t.workspace != nil && t.workspace.Source.GitRepository != "" {
		path, err := gitcredentials.GetHTTPPath(ctx, gitcredentials.GetHttpPathParameters{
			Host:        credentials.Host,
			Protocol:    credentials.Protocol,
			CurrentPath: credentials.Path,
			Repository:  t.workspace.Source.GitRepository,
		})
		if err != nil {
			return nil, fmt.Errorf("get http path: %w", err)
		}
		// Set the credentials `path` field to the path component of the Git repository URL.
		// This allows downstream credential helpers to figure out which passwords needs to be fetched
		credentials.Path = path
	} else {
		log.Warn("workspace is not available for git credentials")
	}

	response, err := gitcredentials.GetCredentials(ctx, credentials)
	if err != nil {
		return nil, fmt.Errorf("get git response: %w", err)
	}
	return response, nil
}

func (t *tunnelServer) platformStreamBlocked() bool {
	return t.platformOptions != nil && t.platformOptions.Enabled && !t.allowPlatformOptions
}

func (t *tunnelServer) workspaceIgnoreExcludes() []string {
	excludes := []string{}
	if t.workspace == nil {
		return excludes
	}

	f, err := os.Open(filepath.Join(t.workspace.Source.LocalFolder, pkgconfig.IgnoreFileName))
	if err == nil {
		defer func() { _ = f.Close() }()
		excludes, err = ignorefile.ReadAll(f)
		if err != nil {
			log.Warnf("error reading %s file: error=%v", pkgconfig.IgnoreFileName, err)
		}
	}
	return excludes
}

func (t *tunnelServer) getResult() *config.Result {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	return t.result
}

func (t *tunnelServer) setResult(result *config.Result) {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	t.result = result
}

// Package external hosts trusted Runtime Protocol v1 executables.
package external

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"google.golang.org/protobuf/proto"
)

const startupTimeout = 15 * time.Second

// Host opens an owned plugin session per operation. It is not yet registered
// as a workspace driver; factory integration is a separate stage.
type Host struct {
	config           provider.ProviderAgentConfig
	binariesDir      string
	supervisorBinary string
	supervisorArgs   []string
	environment      []string
	redactor         *secrets.Redactor
	redactions       *workspaceRedactions
	info             *runtimev1.InfoResponse
	timeout          time.Duration
}

// New discovers a prepared runtime without downloading it and negotiates Info.
// The running Devsy executable supplies the trusted supervisor entry point.
func New(ctx context.Context, workspace *provider.AgentWorkspaceInfo) (*Host, error) {
	if workspace == nil {
		return nil, errors.New("external runtime workspace is missing")
	}
	binariesDir, err := agent.GetAgentBinariesDirFromWorkspaceDir(workspace.Origin)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime binaries directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve Devsy supervisor executable: %w", err)
	}
	environment := provider.ToEnvironment(
		workspace.Workspace,
		workspace.Machine,
		workspace.Options,
		nil,
	)
	return newHost(ctx, hostOptions{
		config:           workspace.Agent,
		directory:        binariesDir,
		supervisorBinary: executable,
		supervisorArgs: []string{
			"internal",
			"runtime-supervisor",
		},
		environment: environment,
		timeout:     startupTimeout,
	})
}

type hostOptions struct {
	config                      provider.ProviderAgentConfig
	directory, supervisorBinary string
	supervisorArgs, environment []string
	timeout                     time.Duration
}

func newHost(ctx context.Context, options hostOptions) (*Host, error) {
	h := &Host{
		config:           options.config,
		binariesDir:      options.directory,
		supervisorBinary: options.supervisorBinary,
		supervisorArgs:   slices.Clone(options.supervisorArgs),
		environment:      slices.Clone(options.environment),
		timeout:          options.timeout,
		redactor:         secrets.NewEnvironmentRedactor(options.environment),
		redactions:       &workspaceRedactions{workspaces: make(map[string]*workspaceRedaction)},
	}
	// Freeze provider declarations so later caller mutation cannot change identity.
	h.config.External.Args = slices.Clone(options.config.External.Args)
	h.config.Binaries = make(map[string][]*provider.ProviderBinary, len(options.config.Binaries))
	for key, locations := range options.config.Binaries {
		for _, binary := range locations {
			if binary == nil {
				h.config.Binaries[key] = append(h.config.Binaries[key], nil)
				continue
			}
			snapshot := *binary
			h.config.Binaries[key] = append(h.config.Binaries[key], &snapshot)
		}
	}
	infoContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()
	err := h.call(infoContext, "Info", func(client runtimev1.RuntimeDriverClient) error {
		info, err := client.Info(infoContext, &runtimev1.InfoRequest{})
		if err != nil {
			return err
		}
		if err := runtimev1.ValidateInfo(info); err != nil {
			return err
		}
		h.info = proto.Clone(info).(*runtimev1.InfoResponse)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

// Info returns a copy of the negotiated capabilities.
func (h *Host) Info() *runtimev1.InfoResponse { return proto.Clone(h.info).(*runtimev1.InfoResponse) }

func (h *Host) executable() (string, error) {
	path, err := provider.ResolveExternalRuntimeBinary(h.config, h.binariesDir)
	if err != nil {
		return "", err
	}
	for _, binary := range h.config.Binaries[h.config.External.Binary] {
		if binary.OS != runtime.GOOS || binary.Arch != runtime.GOARCH ||
			filepath.IsAbs(binary.Path) {
			continue
		}
		if err := containRealPath(
			filepath.Join(h.binariesDir, strings.ToLower(h.config.External.Binary)),
			path,
		); err != nil {
			return "", err
		}
	}
	return path, nil
}

func containRealPath(directory, path string) error {
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return err
	}
	if !filepath.IsLocal(relative) {
		return errors.New("external runtime symlink escapes its binary directory")
	}
	return nil
}

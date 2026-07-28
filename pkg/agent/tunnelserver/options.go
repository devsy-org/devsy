package tunnelserver

import (
	"strings"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
	"github.com/devsy-org/devsy/pkg/netstat"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

type Option func(*tunnelServer) *tunnelServer

func WithWorkspace(workspace *provider2.Workspace) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.workspace = workspace
		return s
	}
}

func WithForwarder(forwarder netstat.Forwarder) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.forwarder = forwarder
		return s
	}
}

func WithAllowGitCredentials(allowGitCredentials bool) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.allowGitCredentials = allowGitCredentials
		return s
	}
}

func WithAllowDockerCredentials(allowDockerCredentials bool) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.allowDockerCredentials = allowDockerCredentials
		return s
	}
}

func WithAllowKubeConfig(allow bool) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.allowKubeConfig = allow
		return s
	}
}

func WithMounts(mounts []*config.Mount) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.mounts = mounts
		return s
	}
}

func WithPlatformOptions(options *devsy.PlatformOptions) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.platformOptions = options
		return s
	}
}

func WithSecrets(env, mount []string) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.secrets = append(toSecrets(env, false), toSecrets(mount, true)...)
		return s
	}
}

func WithGitToken(token *provider2.GitToken) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.gitToken = token
		return s
	}
}

// WithStatusReporter forwards inbound StatusUpdate RPCs to reporter.
func WithStatusReporter(reporter status.Reporter) Option {
	return func(s *tunnelServer) *tunnelServer {
		s.statusReporter = reporter
		return s
	}
}

func toSecrets(entries []string, mount bool) []*tunnel.Secret {
	secrets := make([]*tunnel.Secret, 0, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		secrets = append(secrets, &tunnel.Secret{Name: name, Value: value, Mount: mount})
	}

	return secrets
}

package agent

import (
	"fmt"
	"path/filepath"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

// StateLayout describes how machine workspace state is stored beneath Root.
type StateLayout string

const (
	StateLayoutCanonical StateLayout = "canonical"
	StateLayoutAgentHome StateLayout = "agent-home"
)

// StateLocation identifies the workspace state that an inactivity daemon owns.
type StateLocation struct {
	Root   string
	Layout StateLayout
}

func (l StateLocation) Validate() error {
	if l.Root == "" {
		return fmt.Errorf("daemon state root is required")
	}
	if !filepath.IsAbs(l.Root) {
		return fmt.Errorf("daemon state root must be absolute: %q", l.Root)
	}
	if l.Layout != StateLayoutCanonical && l.Layout != StateLayoutAgentHome {
		return fmt.Errorf("unsupported daemon state layout: %q", l.Layout)
	}
	return nil
}

func (l StateLocation) WorkspaceConfigPattern() (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}

	parts := []string{filepath.Clean(l.Root), "contexts", "*", "workspaces", "*"}
	if l.Layout == StateLayoutCanonical {
		parts = append(parts, "agent")
	}
	parts = append(parts, provider2.WorkspaceConfigFile)
	return filepath.Join(parts...), nil
}

type ResolveStateLocationOptions struct {
	AgentDataPath string
	Origin        string
	Context       string
	WorkspaceID   string
}

// ResolveStateLocation captures the persisted state root at installation time,
// before the daemon is started by systemd under a potentially different user.
func ResolveStateLocation(opts ResolveStateLocationOptions) (StateLocation, error) {
	if opts.AgentDataPath != "" {
		location := StateLocation{
			Root:   filepath.Clean(opts.AgentDataPath),
			Layout: StateLayoutAgentHome,
		}
		return location, location.Validate()
	}

	root, err := CanonicalStateRoot(opts.Origin, opts.Context, opts.WorkspaceID)
	if err != nil {
		return StateLocation{}, err
	}
	return StateLocation{Root: root, Layout: StateLayoutCanonical}, nil
}

// CanonicalStateRoot derives the canonical data root from a persisted
// workspace origin while validating every structural component.
func CanonicalStateRoot(origin, contextName, workspaceID string) (string, error) {
	if contextName == "" {
		contextName = pkgconfig.DefaultContext
	}
	if workspaceID == "" {
		return "", fmt.Errorf("workspace ID is required to resolve daemon state root")
	}

	current := filepath.Clean(origin)
	for _, expected := range []string{"agent", workspaceID, "workspaces", contextName, "contexts"} {
		if filepath.Base(current) != expected {
			return "", fmt.Errorf(
				"unexpected canonical workspace origin %q: expected %q",
				origin,
				expected,
			)
		}
		current = filepath.Dir(current)
	}
	if !filepath.IsAbs(current) {
		return "", fmt.Errorf("canonical daemon state root must be absolute: %q", current)
	}
	return current, nil
}

package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	canonicalTimeoutField = "agent.containerInactivityTimeout"
	legacyTimeoutField    = "agent.kubernetes.inactivityTimeout"

	// LifecycleOwnerManagedDaemon marks a container whose main process is the
	// Devsy daemon and owns inactivity shutdown.
	LifecycleOwnerManagedDaemon = "managedDaemon"
)

// ErrInactivityExpired is returned by the lifecycle loop when the idle timeout
// elapsed. It is the normal way a managed container asks to stop.
var ErrInactivityExpired = errors.New("inactivity timeout reached")

// LifecycleConfig tells the daemon who owns inactivity shutdown.
type LifecycleConfig struct {
	// Owner is LifecycleOwnerManagedDaemon when the daemon is the container's main
	// process and owns the timer.
	Owner string `json:"owner,omitempty"`
	// SetupArmsTimer means the daemon starts unarmed and container setup arms it.
	SetupArmsTimer bool `json:"setupArmsTimer,omitempty"`
}

// Managed reports whether the daemon owns the inactivity timer.
func (l LifecycleConfig) Managed() bool {
	return l.Owner == LifecycleOwnerManagedDaemon
}

// LifecycleOptions are the explicit lifecycle inputs for building a daemon config.
type LifecycleOptions struct {
	// Managed marks the daemon as the container's main process and timer owner.
	Managed bool
	// Timeout is the effective inactivity timeout; zero disables it.
	Timeout time.Duration
}

// ResolveManagedLifecycle selects ownership for a managed-capable driver's topology.
// Dockerless owns the pod process; its nested workspace cannot stop that pod.
func ResolveManagedLifecycle(
	timeout time.Duration,
	dockerless, imageEntrypoint bool,
) (LifecycleOptions, error) {
	if dockerless {
		if timeout > 0 {
			return LifecycleOptions{}, fmt.Errorf(
				"inactivity timeout %s is not supported with dockerless builds because the nested "+
					"workspace container does not end the pod when it exits; unset the inactivity "+
					"timeout or disable dockerless", timeout)
		}
		return LifecycleOptions{}, nil
	}
	if imageEntrypoint {
		if timeout > 0 {
			return LifecycleOptions{}, fmt.Errorf(
				"inactivity timeout %s requires the Devsy daemon to be the container's main "+
					"process, but overrideCommand is false and the image entrypoint would run "+
					"instead; set overrideCommand to true or unset the inactivity timeout", timeout)
		}
		return LifecycleOptions{}, nil
	}
	return LifecycleOptions{Managed: true, Timeout: timeout}, nil
}

// TimeoutInputs are the configured values that decide the effective timeout.
type TimeoutInputs struct {
	// Canonical is agent.containerInactivityTimeout.
	Canonical string
	// Legacy is agent.kubernetes.inactivityTimeout, used only when Canonical is empty.
	Legacy         string
	ShutdownAction string
	DisableDaemon  bool
}

// ResolveInactivityTimeout returns the single effective inactivity timeout for a
// managed container. Zero means disabled.
func ResolveInactivityTimeout(in TimeoutInputs) (time.Duration, error) {
	timeout, err := configuredTimeout(in)
	if err != nil {
		return 0, err
	}
	if timeout == 0 || in.DisableDaemon || in.ShutdownAction == config.ShutdownActionNone {
		return 0, nil
	}
	if in.ShutdownAction == config.ShutdownActionStopCompose {
		return 0, fmt.Errorf(
			"inactivity timeout %s is set but shutdownAction %q is not supported by the kubernetes driver",
			timeout,
			config.ShutdownActionStopCompose,
		)
	}
	return timeout, nil
}

// configuredTimeout merges the canonical field and its legacy alias.
func configuredTimeout(in TimeoutInputs) (time.Duration, error) {
	canonical, err := parseTimeoutField(canonicalTimeoutField, in.Canonical)
	if err != nil {
		return 0, err
	}
	legacy, err := parseTimeoutField(legacyTimeoutField, in.Legacy)
	if err != nil {
		return 0, err
	}

	hasCanonical := strings.TrimSpace(in.Canonical) != ""
	if !hasCanonical {
		return legacy, nil
	}
	if strings.TrimSpace(in.Legacy) != "" && canonical != legacy {
		return 0, fmt.Errorf(
			"%s (%q) conflicts with deprecated alias %s (%q); set only %s",
			canonicalTimeoutField,
			in.Canonical,
			legacyTimeoutField,
			in.Legacy,
			canonicalTimeoutField,
		)
	}
	return canonical, nil
}

func parseTimeoutField(field, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", field, value, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s %q must not be negative", field, value)
	}
	return d, nil
}

// FormatTimeout renders a timeout for DaemonConfig/ContainerWorkspaceInfo; zero is "".
func FormatTimeout(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// DecodeDaemonConfig parses the base64 JSON produced by GetEncodedWorkspaceDaemonConfig.
func DecodeDaemonConfig(encoded string) (*DaemonConfig, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode daemon config: %w", err)
	}
	cfg := &DaemonConfig{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("unmarshal daemon config: %w", err)
	}
	return cfg, nil
}

// EncodeDaemonConfig is the inverse of DecodeDaemonConfig.
func EncodeDaemonConfig(cfg *DaemonConfig) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal daemon config: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// RefreshLifecycle rewrites the generated lifecycle policy of a stored encoded
// daemon config, leaving platform and SSH settings untouched. An empty stored
// config yields a minimal timeout-only config.
func RefreshLifecycle(
	encoded string,
	lifecycle LifecycleOptions,
	shutdownAction string,
) (string, error) {
	cfg := &DaemonConfig{}
	if encoded != "" {
		var err error
		if cfg, err = DecodeDaemonConfig(encoded); err != nil {
			return "", err
		}
	}
	cfg.Timeout = FormatTimeout(lifecycle.Timeout)
	cfg.ShutdownAction = shutdownAction
	cfg.Lifecycle = LifecycleConfig{}
	if lifecycle.Managed {
		cfg.Lifecycle = LifecycleConfig{Owner: LifecycleOwnerManagedDaemon, SetupArmsTimer: true}
	}
	return EncodeDaemonConfig(cfg)
}

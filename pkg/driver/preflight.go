package driver

import (
	"context"
	"os"
	"strconv"
)

// NoAutoStartEnv disables preflight auto-starting a stopped backend.
const NoAutoStartEnv = "DEVSY_NO_AUTOSTART"

// PreflightOptions carries per-invocation preflight settings, passed explicitly
// so behavior is not coupled to process-global state.
type PreflightOptions struct {
	// DisableAutoStart reports whether a stopped backend should be left as-is
	// (reported) rather than started.
	DisableAutoStart bool
}

// Preflighter is implemented by drivers that can validate their backend before
// use and optionally start it. Callers reach it via DriverPreflight.
type Preflighter interface {
	Driver

	Preflight(ctx context.Context, opts PreflightOptions) error
}

// DriverPreflight runs the driver's preflight check, or nothing if it has none.
func DriverPreflight(ctx context.Context, d Driver, opts PreflightOptions) error {
	if p, ok := d.(Preflighter); ok {
		return p.Preflight(ctx, opts)
	}
	return nil
}

// ProvisioningPreflighter is implemented by drivers that need a compatibility
// check before Devsy creates or recreates a devcontainer. It is separate from
// Preflighter because ordinary preflight also runs for lifecycle operations.
type ProvisioningPreflighter interface {
	Driver

	ProvisioningPreflight(ctx context.Context) error
}

// DriverProvisioningPreflight runs a driver's provisioning check when supported.
func DriverProvisioningPreflight(ctx context.Context, d Driver) error {
	if p, ok := d.(ProvisioningPreflighter); ok {
		return p.ProvisioningPreflight(ctx)
	}
	return nil
}

// AutoStartDisabledByEnv reports whether NoAutoStartEnv opts out of auto-start.
func AutoStartDisabledByEnv() bool {
	v, err := strconv.ParseBool(os.Getenv(NoAutoStartEnv))
	return err == nil && v
}

// PreflightError lets callers recognize a backend-readiness failure (errors.As)
// and surface the runtime's own message rather than wrapping it. Provider holds
// the runtime or driver identifier (e.g. "podman", "kubernetes") for grouping.
type PreflightError struct {
	Provider string
	Err      error
}

func (e *PreflightError) Error() string { return e.Err.Error() }
func (e *PreflightError) Unwrap() error { return e.Err }

package microsandbox

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
)

var minimumMicrosandboxRecreateVersion = semver.MustParse("0.7.7")

// Replacement relies on the supported runtime's final validation before teardown,
// independently of the ordinary ownership policy and its provisioning minimum.
func (d *microsandboxDriver) recreationPreflight(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := d.client.Version(ctx)
	if err != nil {
		return &driver.PreflightError{Provider: provider.MicrosandboxDriver, Err: err}
	}
	version, err := parseMicrosandboxVersion(raw)
	if err != nil {
		return &driver.PreflightError{Provider: provider.MicrosandboxDriver, Err: err}
	}
	if version.LT(minimumMicrosandboxRecreateVersion) {
		return &driver.PreflightError{Provider: provider.MicrosandboxDriver, Err: fmt.Errorf(
			"MicroSandbox v%s or newer is required for explicit recreation; update with msb self update",
			minimumMicrosandboxRecreateVersion,
		)}
	}
	return nil
}

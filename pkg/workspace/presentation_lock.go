package workspace

import "github.com/devsy-org/devsy/pkg/config"

// WithPresentationLock runs fn while holding the presentation lock that
// EditPresentation, Rename and ValidateNewWorkspaceID use. fn MUST NOT call
// any function that takes the lock itself; use the Locked variants instead.
func WithPresentationLock(fn func() error) error {
	unlock, err := lockPresentation()
	if err != nil {
		return err
	}
	defer unlock()

	return fn()
}

// ValidateNewWorkspaceIDLocked is ValidateNewWorkspaceID for callers that
// already hold the presentation lock through WithPresentationLock.
func ValidateNewWorkspaceIDLocked(
	devsyConfig *config.Config,
	id string,
	location SSHConfigLocation,
) error {
	return checkWorkspaceIDHost(devsyConfig, id, id, location.withContextDefaults(devsyConfig))
}

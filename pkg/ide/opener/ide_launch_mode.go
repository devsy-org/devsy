package opener

import "fmt"

// IDELaunchMode controls how the openIDE phase runs.
//
//   - LaunchAuto: full launch — start tunnel (for browser IDEs) and open
//     the host browser/app.
//   - LaunchHeadless: do not open the host browser/app. For browser IDEs
//     (openvscode, jupyter, rstudio) the detached tunnel still spawns. For
//     Fleet the workspace-side URL is logged so the user can open it
//     manually. For other desktop IDEs (VSCode flavors, JetBrains, Zed) the
//     openIDE phase only does the host launch, so headless is functionally
//     equivalent to skip — backend install (where one exists) happens
//     during workspace setup, not here.
//   - LaunchSkip: short-circuit the openIDE phase entirely.
type IDELaunchMode string

const (
	LaunchAuto     IDELaunchMode = "auto"
	LaunchHeadless IDELaunchMode = "headless"
	LaunchSkip     IDELaunchMode = "skip"
)

// String implements pflag.Value.
func (m *IDELaunchMode) String() string {
	if m == nil || *m == "" {
		return string(LaunchAuto)
	}
	return string(*m)
}

// Set implements pflag.Value. Strict (case-sensitive) parse to match
// the kubectl/docker convention; returns a descriptive error on bad input.
func (m *IDELaunchMode) Set(v string) error {
	switch IDELaunchMode(v) {
	case LaunchAuto, LaunchHeadless, LaunchSkip:
		*m = IDELaunchMode(v)
		return nil
	default:
		return fmt.Errorf(
			"invalid --ide-launch value %q (must be one of %s, %s, %s)",
			v, LaunchAuto, LaunchHeadless, LaunchSkip,
		)
	}
}

// Type implements pflag.Value.
func (m *IDELaunchMode) Type() string {
	return "auto|headless|skip"
}

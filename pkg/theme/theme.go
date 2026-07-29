// Package theme holds the Devsy CLI color palette and the text styles built
// from it, so terminal output stays consistent with the desktop app.
package theme

import "charm.land/lipgloss/v2"

// Accent is the Devsy purple, matching the desktop app's `.theme-purple`
// primary (hsl(252 100% 68%)). It is a single tone rather than a light/dark
// pair because resolving a pair requires querying the terminal background,
// which needs raw mode — too costly for a `--help` render. This tone clears
// 4.3:1 contrast against both black and white backgrounds.
var Accent = lipgloss.Color("#7C5CFF")

var (
	// Heading styles section headers such as "GLOBAL OPTIONS:".
	Heading = lipgloss.NewStyle().Bold(true)

	// Flag styles a flag name, e.g. "--provider".
	Flag = lipgloss.NewStyle().Foreground(Accent).Bold(true)

	// EnvVar styles a flag's environment variable, e.g. "$DEVSY_PROVIDER".
	EnvVar = lipgloss.NewStyle().Foreground(Accent)

	// Command styles a subcommand name in a command listing.
	Command = lipgloss.NewStyle().Bold(true)

	// Muted styles secondary detail: value types, defaults, footer hints.
	Muted = lipgloss.NewStyle().Faint(true)
)

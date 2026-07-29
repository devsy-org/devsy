// Package theme holds the Devsy CLI color palette and text styles.
package theme

import "charm.land/lipgloss/v2"

// Accent mirrors the desktop app's `.theme-purple` primary, hsl(252 100% 68%).
// A single tone rather than a light/dark pair: resolving a pair means querying
// the terminal background, which needs raw mode — too costly for a --help
// render. This tone clears 4.3:1 contrast on both black and white.
var Accent = lipgloss.Color("#7C5CFF")

var (
	Heading = lipgloss.NewStyle().Bold(true)
	Flag    = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	EnvVar  = lipgloss.NewStyle().Foreground(Accent)
	Command = lipgloss.NewStyle().Bold(true)
	Muted   = lipgloss.NewStyle().Faint(true)
)

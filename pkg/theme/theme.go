// Package theme holds the Devsy CLI color palette and text styles.
package theme

import "charm.land/lipgloss/v2"

var Accent = lipgloss.Color("#7C5CFF")

var (
	Heading = lipgloss.NewStyle().Bold(true)
	Flag    = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	EnvVar  = lipgloss.NewStyle().Foreground(Accent)
	Command = lipgloss.NewStyle().Bold(true)
	Muted   = lipgloss.NewStyle().Faint(true)
)

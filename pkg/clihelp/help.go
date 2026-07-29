// Package clihelp renders `devsy --help` output: uppercase section headers,
// grouped subcommand listings, and flags shown above their wrapped descriptions
// with the Devsy purple accent on flag names and environment variables.
package clihelp

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/theme"
	"github.com/spf13/cobra"
	pflag "github.com/spf13/pflag"
	"golang.org/x/term"
)

const (
	// indent is the left margin for entries inside a section.
	indent = "  "
	// descIndent is the left margin for a flag's description line, set one
	// level deeper than the flag itself so the two read as a pair.
	descIndent = "      "
	// nameColumn is the width reserved for a subcommand name before its
	// short description. Devsy's longest command name is well inside this.
	nameColumn = 12
	// defaultWidth is the wrap width used when stdout is not a terminal.
	defaultWidth = 80
	// maxWidth keeps prose readable on very wide terminals.
	maxWidth = 100
	// minWidth is a floor so wrapping never degenerates on tiny terminals.
	minWidth = 40
)

// Install makes this renderer the help and usage output for cmd and every
// command beneath it. Cobra propagates help/usage functions to children, so a
// single call on the root command covers the whole tree.
func Install(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		render(c, c.OutOrStdout())
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		render(c, c.ErrOrStderr())
		return nil
	})
}

// render writes the full help body for cmd to out, downsampling color to
// whatever the destination supports (and stripping it entirely when the output
// is redirected or NO_COLOR is set).
func render(cmd *cobra.Command, out io.Writer) {
	// os.Environ() is passed explicitly: colorprofile.NewWriter documents a nil
	// environ as meaning os.Environ(), but v0.4.3 builds an empty map instead,
	// so TERM reads as unset and every profile collapses to NoTTY (no color).
	w := colorprofile.NewWriter(out, os.Environ())
	width := wrapWidth(out)

	var b strings.Builder
	writeIntro(&b, cmd, width)
	writeUsage(&b, cmd)
	writeCommands(&b, cmd, width)
	writeFlags(&b, cmd, width)
	writeFooter(&b, cmd)

	_, _ = io.WriteString(w, b.String())
}

// wrapWidth returns the column at which prose should wrap, based on the
// terminal size when out is a terminal.
func wrapWidth(out io.Writer) int {
	f, ok := out.(interface{ Fd() uintptr })
	if !ok {
		return defaultWidth
	}
	cols, _, err := term.GetSize(int(f.Fd())) //nolint:gosec // fd fits in int
	if err != nil || cols <= 0 {
		return defaultWidth
	}
	return clamp(cols, minWidth, maxWidth)
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// writeIntro emits the command's long description, or its short one when no
// long form is set.
func writeIntro(b *strings.Builder, cmd *cobra.Command, width int) {
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	if desc == "" {
		return
	}
	b.WriteString(wrapIndent(desc, width, ""))
	b.WriteString("\n\n")
}

// writeUsage emits the USAGE section, plus any Example block the command
// carries.
func writeUsage(b *strings.Builder, cmd *cobra.Command) {
	section(b, "USAGE")
	if cmd.Runnable() {
		b.WriteString(indent)
		b.WriteString(cmd.UseLine())
		b.WriteString("\n")
	}
	if cmd.HasAvailableSubCommands() {
		b.WriteString(indent)
		b.WriteString(cmd.CommandPath())
		b.WriteString(" [global-flags] <subcommand>\n")
	}
	if cmd.Example != "" {
		b.WriteString("\n")
		b.WriteString(indentBlock(strings.TrimRight(cmd.Example, "\n"), indent))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// writeCommands lists available subcommands, preserving the command groups
// declared on the root command and collecting ungrouped ones under a trailing
// section.
func writeCommands(b *strings.Builder, cmd *cobra.Command, width int) {
	if !cmd.HasAvailableSubCommands() {
		return
	}

	grouped, ungrouped := partitionCommands(cmd)

	for _, group := range cmd.Groups() {
		subs := grouped[group.ID]
		if len(subs) == 0 {
			continue
		}
		section(b, sectionTitle(group.Title))
		writeCommandList(b, subs, width)
		b.WriteString("\n")
	}

	if len(ungrouped) > 0 {
		title := "SUBCOMMANDS"
		if len(grouped) > 0 {
			title = "ADDITIONAL COMMANDS"
		}
		section(b, title)
		writeCommandList(b, ungrouped, width)
		b.WriteString("\n")
	}
}

// partitionCommands splits cmd's visible subcommands into those belonging to a
// declared group, keyed by group ID, and those without one.
func partitionCommands(cmd *cobra.Command) (map[string][]*cobra.Command, []*cobra.Command) {
	grouped := map[string][]*cobra.Command{}
	var ungrouped []*cobra.Command
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() && !sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if sub.GroupID != "" && cmd.ContainsGroup(sub.GroupID) {
			grouped[sub.GroupID] = append(grouped[sub.GroupID], sub)
			continue
		}
		ungrouped = append(ungrouped, sub)
	}
	return grouped, ungrouped
}

// sectionTitle normalizes a cobra group title ("Core commands:") into a section
// header ("CORE COMMANDS").
func sectionTitle(groupTitle string) string {
	return strings.ToUpper(strings.TrimRight(strings.TrimSpace(groupTitle), ":"))
}

func writeCommandList(b *strings.Builder, subs []*cobra.Command, width int) {
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, sub := range subs {
		name := sub.Name()
		b.WriteString(indent)
		b.WriteString(theme.Command.Render(name))
		if sub.Short == "" {
			b.WriteString("\n")
			continue
		}
		// Pad from the plain name — the styled string carries escape bytes
		// that would otherwise be counted as visible columns.
		b.WriteString(strings.Repeat(" ", max(1, nameColumn-len(name))))
		descCol := len(indent) + nameColumn
		b.WriteString(wrapHanging(sub.Short, width, descCol))
		b.WriteString("\n")
	}
}

// writeFlags emits the command's own flags under OPTIONS and the global flags
// under GLOBAL OPTIONS. Globals are the ones this command declares as
// persistent (on the root) plus everything inherited from ancestors — both
// apply to subcommands, so they belong in the same section.
func writeFlags(b *strings.Builder, cmd *cobra.Command, width int) {
	persistent := cmd.PersistentFlags()
	var local, global []*pflag.Flag
	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if persistent.Lookup(f.Name) != nil {
			global = append(global, f)
			return
		}
		local = append(local, f)
	})
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			global = append(global, f)
		}
	})

	if len(local) > 0 {
		section(b, "OPTIONS")
		writeFlagList(b, local, width)
	}
	if len(global) > 0 {
		section(b, "GLOBAL OPTIONS")
		b.WriteString(wrapIndent(
			"Global options apply to every subcommand. Each may be set by flag "+
				"or by its environment variable.",
			width, indent,
		))
		b.WriteString("\n\n")
		writeFlagList(b, global, width)
	}
}

// writeFlagList renders each flag as a signature line — name, value type, env
// var, default — followed by its indented, wrapped description.
func writeFlagList(b *strings.Builder, list []*pflag.Flag, width int) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for _, f := range list {
		b.WriteString(indent)
		b.WriteString(flagSignature(f))
		b.WriteString("\n")
		if usage := flagUsage(f); usage != "" {
			b.WriteString(wrapIndent(usage, width, descIndent))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

// flagSignature builds the styled first line for a flag, e.g.
// "-v, --verbose count, $DEVSY_VERBOSE (default: 0)".
func flagSignature(f *pflag.Flag) string {
	var parts []string
	if f.Shorthand != "" {
		parts = append(parts, theme.Flag.Render("-"+f.Shorthand))
	}
	name := theme.Flag.Render("--" + f.Name)
	if valueType, _ := pflag.UnquoteUsage(f); valueType != "" {
		name += " " + theme.Muted.Render(valueType)
	}
	parts = append(parts, name)

	sig := strings.Join(parts, ", ")
	if env := f.Annotations[flags.EnvAnnotation]; len(env) > 0 {
		sig += theme.Muted.Render(", ") + theme.EnvVar.Render("$"+env[0])
	}
	if def := defaultText(f); def != "" {
		sig += " " + theme.Muted.Render(def)
	}
	return sig
}

// defaultText renders a flag's default as "(default: x)", omitting zero values
// that carry no information.
func defaultText(f *pflag.Flag) string {
	switch f.DefValue {
	case "", "false", "0", "[]", "0s":
		return ""
	}
	return fmt.Sprintf("(default: %s)", f.DefValue)
}

// flagUsage returns a flag's description with the value-type backquotes that
// pflag uses for naming stripped out.
func flagUsage(f *pflag.Flag) string {
	_, usage := pflag.UnquoteUsage(f)
	return strings.TrimSpace(usage)
}

func writeFooter(b *strings.Builder, cmd *cobra.Command) {
	if !cmd.HasAvailableSubCommands() {
		return
	}
	b.WriteString(theme.Muted.Render(fmt.Sprintf(
		"Run %q for more information about a subcommand.",
		cmd.CommandPath()+" <subcommand> --help",
	)))
	b.WriteString("\n")
}

func section(b *strings.Builder, title string) {
	b.WriteString(theme.Heading.Render(title + ":"))
	b.WriteString("\n")
}

// wrapIndent wraps text to width and prefixes every resulting line with prefix.
func wrapIndent(text string, width int, prefix string) string {
	limit := max(minWidth/2, width-len(prefix))
	wrapped := ansi.Wordwrap(strings.TrimSpace(text), limit, " -")
	return indentBlock(wrapped, prefix)
}

// wrapHanging wraps text to width assuming the first line already begins at
// column col, indenting continuation lines to line up under it.
func wrapHanging(text string, width, col int) string {
	limit := max(minWidth/2, width-col)
	lines := strings.Split(ansi.Wordwrap(strings.TrimSpace(text), limit, " -"), "\n")
	pad := strings.Repeat(" ", col)
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// indentBlock prefixes every non-empty line of text with prefix.
func indentBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

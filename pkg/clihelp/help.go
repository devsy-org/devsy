// Package clihelp renders `devsy --help` output.
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
	indent       = "  "
	descIndent   = "      "
	nameColumn   = 12
	defaultWidth = 80
	maxWidth     = 100
	minWidth     = 40
)

// Install applies this renderer to cmd and, since cobra propagates help and
// usage functions to children, every command beneath it.
func Install(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		render(c, c.OutOrStdout())
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		render(c, c.ErrOrStderr())
		return nil
	})
}

func render(cmd *cobra.Command, out io.Writer) {
	// colorprofile.NewWriter documents a nil environ as meaning os.Environ(),
	// but v0.4.3 builds an empty map instead, so TERM reads as unset and every
	// profile collapses to NoTTY, silently stripping all color.
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

func writeCommands(b *strings.Builder, cmd *cobra.Command, width int) {
	var subs []*cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			subs = append(subs, sub)
		}
	}
	if len(subs) == 0 {
		return
	}

	section(b, "SUBCOMMANDS")
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
	b.WriteString("\n")
}

// A command's own persistent flags apply to its subcommands just as inherited
// ones do, so both are listed as global.
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

// Zero-ish defaults carry no information, so they are omitted to keep
// signature lines scannable.
func defaultText(f *pflag.Flag) string {
	switch f.DefValue {
	case "", "false", "0", "[]", "0s":
		return ""
	}
	return fmt.Sprintf("(default: %s)", f.DefValue)
}

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

func wrapIndent(text string, width int, prefix string) string {
	limit := max(minWidth/2, width-len(prefix))
	wrapped := ansi.Wordwrap(strings.TrimSpace(text), limit, " -")
	return indentBlock(wrapped, prefix)
}

// wrapHanging assumes the first line already begins at column col, so only
// continuation lines are padded to line up under it.
func wrapHanging(text string, width, col int) string {
	limit := max(minWidth/2, width-col)
	lines := strings.Split(ansi.Wordwrap(strings.TrimSpace(text), limit, " -"), "\n")
	pad := strings.Repeat(" ", col)
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func indentBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

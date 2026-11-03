package clihelp

import (
	"bytes"
	"strings"
	"testing"

	pkgflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "devsy", Short: "Devsy", Long: "Devsy long description."}

	pf := root.PersistentFlags()
	pf.String("provider", "", "The provider to use")
	pkgflags.BindEnv(pf, "provider")
	pf.Bool("hidden-global", false, "should not appear")
	require.NoError(t, pf.MarkHidden("hidden-global"))

	noop := func(*cobra.Command, []string) {}
	ws := &cobra.Command{Use: "workspace", Aliases: []string{"ws"}, Short: "Manage workspaces"}
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List workspaces", Run: noop,
	}
	list.Flags().Bool("skip-platform", false, "Don't list platform workspaces")
	ws.AddCommand(list)
	root.AddCommand(ws)
	root.AddCommand(&cobra.Command{Use: "loose", Short: "A loose command", Run: noop})

	Install(root)
	return root
}

// A bytes.Buffer is not a terminal, so colorprofile strips styling and
// assertions can match plain text.
func renderHelp(t *testing.T, root *cobra.Command, path ...string) string {
	t.Helper()
	target := root
	if len(path) > 0 {
		found, _, err := root.Find(path)
		require.NoError(t, err)
		target = found
	}
	var buf bytes.Buffer
	target.SetOut(&buf)
	target.SetErr(&buf)
	require.NoError(t, target.Help())
	return buf.String()
}

func TestRender_RootSections(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))

	assert.Contains(t, out, "Devsy long description.")
	assert.Contains(t, out, "USAGE:")
	assert.Contains(t, out, "devsy [global-flags] <subcommand>")
	assert.Contains(t, out, "SUBCOMMANDS:")
	assert.Contains(t, out, "GLOBAL OPTIONS:")
	assert.Contains(t, out, "Run \"devsy <subcommand> --help\"")
}

func TestRender_SubcommandsAreFlatAndSorted(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))

	body := sectionBody(t, out, "SUBCOMMANDS:")
	assert.Contains(t, body, "loose")
	assert.Contains(t, body, "workspace")
	assert.Less(t, strings.Index(body, "loose"), strings.Index(body, "workspace"))
	assert.NotContains(t, out, "CORE COMMANDS:")
	assert.NotContains(t, out, "ADDITIONAL COMMANDS:")
}

func TestRender_EnvVarOnSignatureLine(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))

	require.Contains(t, out, "--provider string, $DEVSY_PROVIDER")
	line := signatureLine(t, out, "--provider")
	assert.NotContains(t, line, "The provider to use", "description belongs on its own line")
}

func TestRender_HidesHiddenFlags(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))
	assert.NotContains(t, out, "hidden-global")
}

func TestRender_RootPersistentFlagsAreGlobal(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))
	assert.Contains(t, sectionBody(t, out, "GLOBAL OPTIONS:"), "--provider")
}

func TestRender_LeafSeparatesLocalFromGlobal(t *testing.T) {
	out := renderHelp(t, newTestRoot(t), "workspace", "list")

	assert.Contains(t, out, "devsy workspace list [flags]")
	assert.Contains(t, sectionBody(t, out, "OPTIONS:"), "--skip-platform")
	assert.Contains(t, sectionBody(t, out, "GLOBAL OPTIONS:"), "--provider")
	// A leaf has no subcommands, so the footer hint would be a dead end.
	assert.NotContains(t, out, "for more information about a subcommand")
}

func TestDefaultText_OmitsZeroValues(t *testing.T) {
	root := &cobra.Command{Use: "x", Run: func(*cobra.Command, []string) {}}
	fs := root.Flags()
	fs.String("empty", "", "empty default")
	fs.Bool("off", false, "false default")
	fs.Int("zero", 0, "zero default")
	fs.String("set", "text", "real default")
	Install(root)

	out := renderHelp(t, root)
	for _, flagName := range []string{"--empty", "--off", "--zero"} {
		assert.NotContains(t, signatureLine(t, out, flagName), "(default:")
	}
	assert.Contains(t, signatureLine(t, out, "--set"), "(default: text)")
}

func TestRender_NoColorWhenNotATerminal(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))
	assert.NotContains(t, out, "\x1b[", "styling must be stripped for non-terminal output")
}

func signatureLine(t *testing.T, out, flagName string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, flagName) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", flagName, out)
	return ""
}

// Section headers are the only unindented, uppercase, colon-terminated lines.
func sectionBody(t *testing.T, out, header string) string {
	t.Helper()
	_, rest, found := strings.Cut(out, header)
	require.True(t, found, "section %q not found in:\n%s", header, out)

	var body []string
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.HasSuffix(line, ":") && line == strings.ToUpper(line) &&
			!strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" {
			break
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}

func TestRender_SubcommandListShowsAliases(t *testing.T) {
	root := newTestRoot(t)

	assert.Contains(t, sectionBody(t, renderHelp(t, root), "SUBCOMMANDS:"), "workspace (ws)")
	assert.Contains(
		t,
		sectionBody(t, renderHelp(t, root, "workspace"), "SUBCOMMANDS:"),
		"list (ls)",
	)
}

func TestRender_AliasesSection(t *testing.T) {
	root := newTestRoot(t)

	body := sectionBody(t, renderHelp(t, root, "workspace", "list"), "ALIASES:")
	assert.Equal(t, "devsy workspace ls", strings.TrimSpace(body))
	assert.NotContains(t, renderHelp(t, root, "loose"), "ALIASES:")
}

func TestRender_SubcommandDescriptionsAlign(t *testing.T) {
	root := newTestRoot(t)
	root.AddCommand(&cobra.Command{
		Use: "resolve-dependencies", Short: "Resolve order", Run: func(*cobra.Command, []string) {},
	})

	body := sectionBody(t, renderHelp(t, root), "SUBCOMMANDS:")
	assert.Contains(t, body, "resolve-dependencies  Resolve order")
	assert.Contains(t, body, "loose                 A loose command")
	assert.Contains(t, body, "workspace (ws)        Manage workspaces")
}

func TestRender_LongAliasedLabelStaysInsideWidth(t *testing.T) {
	root := newTestRoot(t)
	root.AddCommand(&cobra.Command{
		Use:     "describe",
		Aliases: []string{"show", "inspect", "details", "info"},
		Short:   "Show details",
		Run:     func(*cobra.Command, []string) {},
	})

	body := sectionBody(t, renderHelp(t, root), "SUBCOMMANDS:")
	for line := range strings.SplitSeq(body, "\n") {
		assert.LessOrEqual(t, len(line), defaultWidth, line)
	}
	assert.Contains(t, body, "describe (show, inspect, details, info)\n")
	assert.Contains(t, body, "\n"+strings.Repeat(" ", 28)+"Show details")
}

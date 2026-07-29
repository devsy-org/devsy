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

// newTestRoot builds a small command tree that mirrors the real one: grouped
// subcommands, a persistent flag with an env var, and a local flag on a leaf.
func newTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "devsy", Short: "Devsy", Long: "Devsy long description."}
	root.AddGroup(&cobra.Group{ID: "core", Title: "Core commands:"})

	pf := root.PersistentFlags()
	pf.String("provider", "", "The provider to use")
	pkgflags.BindEnv(pf, "provider")
	pf.Bool("hidden-global", false, "should not appear")
	require.NoError(t, pf.MarkHidden("hidden-global"))

	noop := func(*cobra.Command, []string) {}
	ws := &cobra.Command{Use: "workspace", Short: "Manage workspaces", GroupID: "core"}
	list := &cobra.Command{Use: "list", Short: "List workspaces", Run: noop}
	list.Flags().Bool("skip-pro", false, "Don't list pro workspaces")
	ws.AddCommand(list)
	root.AddCommand(ws)
	root.AddCommand(&cobra.Command{Use: "loose", Short: "Ungrouped command", Run: noop})

	Install(root)
	return root
}

// renderHelp captures help output for the command at path. Output goes to a
// bytes.Buffer, which is not a terminal, so colorprofile strips all styling and
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

func TestRender_RootSectionsAndGroups(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))

	assert.Contains(t, out, "Devsy long description.")
	assert.Contains(t, out, "USAGE:")
	assert.Contains(t, out, "devsy [global-flags] <subcommand>")
	assert.Contains(t, out, "CORE COMMANDS:")
	assert.Contains(t, out, "workspace")
	// Commands with no group land in a trailing section, not the group listing.
	assert.Contains(t, out, "ADDITIONAL COMMANDS:")
	assert.Contains(t, out, "loose")
	assert.Contains(t, out, "GLOBAL OPTIONS:")
	assert.Contains(t, out, "Run \"devsy <subcommand> --help\"")
}

// The env var belongs on the flag's signature line, beside the flag name, so it
// can carry the purple accent independently of the description text.
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

// A root command's own persistent flags are global to the whole tree, so they
// must be listed under GLOBAL OPTIONS rather than the root's local OPTIONS.
func TestRender_RootPersistentFlagsAreGlobal(t *testing.T) {
	out := renderHelp(t, newTestRoot(t))
	assert.Contains(t, sectionBody(t, out, "GLOBAL OPTIONS:"), "--provider")
}

func TestRender_LeafSeparatesLocalFromGlobal(t *testing.T) {
	out := renderHelp(t, newTestRoot(t), "workspace", "list")

	assert.Contains(t, out, "devsy workspace list [flags]")
	assert.Contains(t, sectionBody(t, out, "OPTIONS:"), "--skip-pro")
	assert.Contains(t, sectionBody(t, out, "GLOBAL OPTIONS:"), "--provider")
	// A leaf has no subcommands, so the footer hint would be a dead end.
	assert.NotContains(t, out, "for more information about a subcommand")
}

// Defaults that carry no information are omitted so signature lines stay scannable.
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

// signatureLine returns the rendered line containing the given flag name.
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

// section returns the body of a named section, up to the next section header.
func sectionBody(t *testing.T, out, header string) string {
	t.Helper()
	_, rest, found := strings.Cut(out, header)
	require.True(t, found, "section %q not found in:\n%s", header, out)
	for _, next := range []string{"OPTIONS:", "GLOBAL OPTIONS:", "COMMANDS:"} {
		if body, _, cut := strings.Cut(rest, next); cut {
			rest = body
		}
	}
	return rest
}

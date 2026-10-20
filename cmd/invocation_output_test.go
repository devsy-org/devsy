package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

const (
	inspectTestPodman     = "podman"
	inspectTestKubernetes = "kubernetes"
	inspectTestGroupAlias = "deps"
	inspectTestLeafAlias  = "look"

	inspectTestGroup        = "dependency"
	inspectTestLeaf         = "inspect"
	inspectTestDocker       = "docker"
	inspectTestApple        = "apple"
	inspectTestContext      = "chosen"
	inspectTestJSON         = "--json"
	inspectTestJSONFalse    = "--json=false"
	inspectTestBadJSON      = "--json=bad"
	inspectTestForProvider  = "--for-provider"
	inspectTestDockerTarget = "--for-provider=docker-installed"
	inspectTestPanic        = "panic"
	inspectTestCanceled     = "canceled"
)

func inspectionReaderRoot() (*cobra.Command, *cobra.Command, *bool) {
	called := false
	root := &cobra.Command{Use: "fixture", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.PersistentFlags().String("provider", "environment-decoy", "")
	root.PersistentFlags().Lookup("provider").Changed = true
	root.PersistentFlags().StringP("home", "H", "", "")
	root.PersistentFlags().BoolP("quiet", "q", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	group := &cobra.Command{Use: inspectTestGroup, Aliases: []string{inspectTestGroupAlias}}
	leaf := &cobra.Command{
		Use: inspectTestLeaf, Aliases: []string{inspectTestLeafAlias}, Args: cobra.ArbitraryArgs,
		Run: func(_ *cobra.Command, _ []string) { called = true },
	}
	leaf.Flags().BoolP("json", "j", false, "")
	leaf.Flags().StringP("for-provider", "f", "", "")
	flags.MarkDependencyInspection(group)
	flags.MarkDependencyInspection(leaf)
	flags.MarkPassive(group)
	flags.MarkPassive(leaf)
	group.AddCommand(
		leaf,
		&cobra.Command{Use: "active", Run: func(_ *cobra.Command, _ []string) {}},
	)
	root.AddCommand(group)
	return root, leaf, &called
}

func flagStates(command *cobra.Command) map[string]string {
	states := map[string]string{}
	for current := command; current != nil; current = current.Parent() {
		for _, set := range []*pflag.FlagSet{current.Flags(), current.PersistentFlags()} {
			set.VisitAll(func(flag *pflag.Flag) {
				states[flag.Name] = flag.Value.String()
				if flag.Changed {
					states[flag.Name] += ":changed"
				}
			})
		}
	}
	return states
}

//nolint:funlen // Literal argv matrix covers the audited framing and provenance cases.
func TestInspectionInvocationReader(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		json       bool
		provider   bool
		value      string
		parseError bool
		runs       bool
	}{
		{"bare", []string{inspectTestDocker, inspectTestJSON}, true, false, "", false, true},
		{"true", []string{"--json=true", inspectTestDocker}, true, false, "", false, true},
		{"false", []string{inspectTestDocker, inspectTestJSONFalse}, false, false, "", false, true},
		{"repeated-false", []string{inspectTestJSON, inspectTestDocker, inspectTestJSONFalse}, false, false, "", false, true},
		{"repeated-true", []string{inspectTestJSONFalse, inspectTestDocker, inspectTestJSON}, true, false, "", false, true},
		{"invalid", []string{inspectTestDocker, "--json=garbage"}, true, false, "", true, false},
		{"invalid-then-false", []string{inspectTestBadJSON, inspectTestJSONFalse}, true, false, "", true, false},
		{"equals-value", []string{inspectTestDocker, "--for-provider=--json"}, false, false, "", false, true},
		{
			"separate-value",
			[]string{inspectTestDocker, inspectTestForProvider, inspectTestJSON},
			false, false, "", false, true,
		},
		{
			"delimiter",
			[]string{inspectTestDocker, "--", inspectTestJSON, "--provider=operand"},
			false, false, "", false, true,
		},
		{"unknown-before", []string{inspectTestDocker, "--unknown", inspectTestJSON}, true, false, "", true, false},
		{"bad-bool-before", []string{inspectTestDocker, "--quiet=bad", inspectTestJSON}, true, false, "", true, false},
		{"bad-count-before", []string{inspectTestDocker, "-v=bad", inspectTestJSON}, true, false, "", true, false},
		{"cluster", []string{inspectTestDocker, "-qvj"}, true, false, "", false, true},
		{"cluster-value", []string{inspectTestDocker, "-qf--json"}, false, false, "", false, true},
		{"cluster-required", []string{inspectTestDocker, "-qH", inspectTestJSON}, false, false, "", false, true},
		{"short-invalid", []string{"-j=bad", inspectTestJSONFalse}, true, false, "", true, false},
		{"provider", []string{"--provider=renamed", inspectTestDocker, inspectTestJSON}, true, true, "renamed", false, true},
		{"provider-empty", []string{inspectTestDocker, "--provider=", inspectTestJSON}, true, true, "", false, true},
		{
			"provider-value",
			[]string{inspectTestDocker, passiveTestHomeFlag, "--provider=decoy"},
			false, false, "", false, true,
		},
		{
			"provider-repeat",
			[]string{"--provider=first", inspectTestDocker, "--provider", "last"},
			false, true, "last", false, true,
		},
		{passiveTestHelp, []string{inspectTestDocker, passiveTestHelpFlag, inspectTestJSON}, true, false, "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, leaf, called := inspectionReaderRoot()
			args := append([]string{inspectTestGroup, inspectTestLeaf}, test.args...)
			target, remaining := resolveInvocationCommand(root, args)
			require.Same(t, leaf, target)
			before := flagStates(target)
			invocation := readInspectionInvocation(target, remaining)
			require.Equal(t, before, flagStates(target), "reader changed flag state")
			require.Equal(
				t,
				flags.InspectionInvocation{
					JSON:            test.json,
					ProviderPresent: test.provider,
					Provider:        test.value,
				},
				invocation,
			)
			root.SetArgs(args)
			selected, err := root.ExecuteC()
			require.Equal(t, test.parseError, err != nil)
			require.Same(t, target, selected)
			require.Equal(t, test.runs, *called)
		})
	}
}

func TestInspectionInvocationScope(t *testing.T) {
	for _, test := range []struct {
		args    []string
		json    bool
		passive bool
	}{
		{[]string{inspectTestGroupAlias, inspectTestLeafAlias, inspectTestDocker, inspectTestJSON}, true, true},
		{[]string{
			passiveTestHomeFlag, inspectTestGroup,
			inspectTestGroupAlias, inspectTestLeafAlias, inspectTestJSON,
		}, true, true},
		{[]string{inspectTestGroup, passiveTestHelpFlag}, false, true},
		{[]string{passiveTestHelp, inspectTestGroup, inspectTestLeaf}, false, true},
		{[]string{inspectTestGroup, "active", inspectTestJSON}, false, false},
		{[]string{"__complete", inspectTestGroup, inspectTestLeaf, inspectTestJSON}, false, false},
	} {
		root, _, _ := inspectionReaderRoot()
		target, remaining := resolveInvocationCommand(root, test.args)
		require.Equal(t, test.json, readInspectionInvocation(target, remaining).JSON)
		require.Equal(t, test.passive, passiveCommand(target, remaining))
	}
}

func TestInspectionFramingBeforeOutput(t *testing.T) {
	root, leaf, _ := inspectionReaderRoot()
	global := &flags.GlobalFlags{}
	root.SetContext(flags.WithInspectionInvocation(withInvocationPolicy(context.Background(), true),
		readInspectionInvocation(leaf, []string{inspectTestJSON})))
	require.True(t, configureOutput(root, global, false))
	require.True(t, root.SilenceUsage)
}

func TestInspectionStartupCancellation(t *testing.T) {
	command := &cobra.Command{Use: inspectTestLeaf}
	flags.MarkDependencyInspection(command)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command.SetContext(ctx)
	require.ErrorIs(
		t,
		inspectionStartupError(command, errors.New("private-loader-path")),
		context.Canceled,
	)
	command.SetContext(context.Background())
	require.EqualError(t, inspectionStartupError(command, errors.New("private-loader-path")),
		"cannot prepare passive dependency inspection inputs")
}

func TestInspectionOuterCleanup(t *testing.T) {
	for _, mode := range []string{"error", inspectTestPanic, inspectTestCanceled} {
		t.Run(mode, func(t *testing.T) {
			dir, home := passiveFixtureEnvironment(t)
			preservePassiveFixture(t, dir)
			originalArgs, originalStderr := os.Args, os.Stderr
			t.Cleanup(func() { os.Args, os.Stderr = originalArgs, originalStderr })
			os.Args = []string{
				config.BinaryName,
				passiveTestCommand,
				inspectTestJSON,
				passiveTestHomeFlag,
				home,
			}
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			defer func() { require.NoError(t, reader.Close()) }()
			os.Stderr = writer
			code := runWithRootFactory(func() (*cobra.Command, *flags.GlobalFlags) {
				root, global := passiveTestRoot(func(command *cobra.Command, _ []string) error {
					require.True(t, flags.InspectionInvocationFromContext(command.Context()).JSON)
					if mode == inspectTestPanic {
						panic("private-panic-credential")
					}
					if mode == inspectTestCanceled {
						return context.Canceled
					}
					return errors.New("fixed inspection fixture error")
				})
				leaf, _, err := root.Find([]string{passiveTestCommand})
				require.NoError(t, err)
				flags.MarkDependencyInspection(leaf)
				leaf.Flags().Bool("json", false, "")
				return root, global
			})
			require.NoError(t, writer.Close())
			os.Stderr = originalStderr
			raw, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, 1, code)
			require.True(t, json.Valid(raw), "%s", raw)
			require.NotContains(t, string(raw), "private-panic-credential")
			if mode == inspectTestCanceled {
				var envelope struct {
					Code string `json:"code"`
				}
				require.NoError(t, json.Unmarshal(raw, &envelope))
				require.Equal(t, inspectTestCanceled, envelope.Code)
			}
		})
	}
}

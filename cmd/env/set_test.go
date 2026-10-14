package env

import (
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/stretchr/testify/require"
)

const (
	setCommandTestName           = "SET_COMMAND_TEST_VALUE"
	setCommandTestInline         = setCommandTestName + "=" + isolationTestEnvValue
	setCommandTestEmptyInline    = setCommandTestName + "="
	setCommandTestValueFlag      = "--value"
	setCommandTestEmptyValueFlag = setCommandTestValueFlag + "="
)

func TestSetCommandAssignment(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		value string
	}{
		{name: "inline", args: []string{setCommandTestInline}, value: isolationTestEnvValue},
		{name: "inline empty", args: []string{setCommandTestEmptyInline}},
		{name: "inline additional equals", args: []string{setCommandTestEmptyInline + "a=b"}, value: "a=b"},
		{
			name:  "flag",
			args:  []string{setCommandTestName, setCommandTestValueFlag, isolationTestEnvValue},
			value: isolationTestEnvValue,
		},
		{name: "flag empty", args: []string{setCommandTestName, setCommandTestEmptyValueFlag}},
		{name: "bare name", args: []string{setCommandTestName}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flags := setupEnvCommandTest(t)
			cmd := NewSetCmd(flags)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())

			cfg := mustLoadConfig(t)
			store, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			value, err := store.Get(cfg.DefaultContext, setCommandTestName)
			require.NoError(t, err)
			require.Equal(t, tt.value, value)
		})
	}
}

func TestSetCommandRejectsInvalidAssignment(t *testing.T) {
	const invalidTestName = "BAD-NAME"
	const invalidInline = invalidTestName + "=" + isolationTestEnvValue
	const conflict = "specify the value inline as NAME=VALUE or with --value, not both"
	const invalidName = "invalid environment variable name \"BAD-NAME\": must start with a letter or underscore and " +
		"contain only letters, digits, and underscores"
	for _, tt := range []struct {
		name string
		args []string
		err  string
	}{
		{name: "inline and flag", args: []string{setCommandTestInline, setCommandTestValueFlag, "trace"}, err: conflict},
		{name: "inline and empty flag", args: []string{setCommandTestInline, setCommandTestEmptyValueFlag}, err: conflict},
		{
			name: "empty inline and empty flag",
			args: []string{setCommandTestEmptyInline, setCommandTestEmptyValueFlag},
			err:  conflict,
		},
		{name: "invalid bare name", args: []string{invalidTestName}, err: invalidName},
		{name: "invalid inline name", args: []string{invalidInline}, err: invalidName},
		{name: "conflict before validation", args: []string{invalidInline, setCommandTestEmptyValueFlag}, err: conflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flags := setupEnvCommandTest(t)
			cfg := mustLoadConfig(t)
			store, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(t, store.Set(cfg.DefaultContext, setCommandTestName, "existing"))

			cmd := NewSetCmd(flags)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			require.EqualError(t, cmd.Execute(), tt.err)

			values, err := store.List(cfg.DefaultContext)
			require.NoError(t, err)
			require.Len(t, values, 1)
			require.Equal(t, setCommandTestName, values[0].Name)
			require.Equal(t, "existing", values[0].Value)
		})
	}
}

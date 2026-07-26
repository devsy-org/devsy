package secrets

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/survey"
	"github.com/spf13/cobra"
)

type SetCmd struct {
	*flags.GlobalFlags

	Value    string
	valueSet bool
	FromFile string
	Stdin    bool
}

func NewSetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{
		GlobalFlags: flags,
	}
	setCmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or update a secret",
		Long: `Create or update a secret in the active context.

The value can be supplied with --value, piped via --stdin, read from a file
with --from-file, or entered at a hidden interactive prompt when none is given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			cmd.valueSet = cobraCmd.Flags().Changed(names.Value)
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	cliflags.Add(
		setCmd,
		cliflags.String(
			&cmd.Value, names.Value, "",
			"The secret value (prefer --stdin for sensitive values)",
		),
		cliflags.String(&cmd.FromFile, names.FromFile, "", "Read the secret value from a file"),
		cliflags.Bool(&cmd.Stdin, names.Stdin, false, "Read the secret value from standard input"),
	)
	return setCmd
}

func (cmd *SetCmd) Run(_ context.Context, name string) error {
	if err := secrets.ValidateName(name); err != nil {
		return err
	}

	value, err := cmd.resolveValue(name)
	if err != nil {
		return err
	}

	contextName, store, err := resolveContext(cmd.GlobalFlags)
	if err != nil {
		return err
	}

	if err := store.Set(contextName, name, value, secrets.KindSecret); err != nil {
		return err
	}

	log.Infof("secret %q set in context %q", name, contextName)
	return nil
}

func (cmd *SetCmd) resolveValue(name string) (string, error) {
	sources := 0
	for _, on := range []bool{cmd.valueSet, cmd.FromFile != "", cmd.Stdin} {
		if on {
			sources++
		}
	}
	if sources > 1 {
		return "", fmt.Errorf("specify only one of --value, --from-file, or --stdin")
	}

	switch {
	case cmd.valueSet:
		return cmd.Value, nil
	case cmd.FromFile != "":
		return readTrimmedFile(cmd.FromFile)
	case cmd.Stdin:
		return readTrimmed(os.Stdin, "stdin")
	default:
		return survey.NewSurvey().Question(&survey.QuestionOptions{
			Question:   fmt.Sprintf("Enter value for secret %q", name),
			IsPassword: true,
		})
	}
}

func readTrimmedFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- user-specified secret source file.
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return readTrimmed(f, "secret file")
}

func readTrimmed(r io.Reader, source string) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", source, err)
	}

	// Strip only one trailing transport newline so a value that intentionally
	// ends in blank lines (e.g. a PEM file) is preserved.
	value := string(data)
	if trimmed, ok := strings.CutSuffix(value, "\r\n"); ok {
		return trimmed, nil
	}
	return strings.TrimSuffix(value, "\n"), nil
}

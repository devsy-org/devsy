package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/survey"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	protectionRemember         = "remember"
	protectionSetPassphrase    = "set-passphrase"
	protectionChangePassphrase = "change-passphrase"
)

func NewProtectionCmd(global *flags.GlobalFlags) *cobra.Command {
	parent := &cobra.Command{
		Use:   "protection",
		Short: "Manage encrypted file protection and unlock credentials",
	}
	parent.AddCommand(newProtectionStatusCmd(global))
	for _, action := range []string{protectionRemember, protectionSetPassphrase, protectionChangePassphrase} {
		parent.AddCommand(newProtectionInputCmd(action))
	}
	parent.AddCommand(
		newProtectionMutationCmd(
			"forget",
			"Forget the opt-in OS-keychain passphrase",
			(*secrets.ProtectionManager).Forget,
		),
	)
	parent.AddCommand(
		newProtectionMutationCmd(
			"remove-passphrase",
			"Re-encrypt the file store with an automatic key",
			(*secrets.ProtectionManager).RemovePassphrase,
		),
	)
	parent.AddCommand(newProtectionResetCmd())
	return parent
}

func protectionResolver() secrets.UnlockMaterialResolver {
	return secrets.DefaultUnlockResolver{
		Prompt: func(_ context.Context, _ secrets.UnlockRequest) (string, error) {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return "", &secrets.UnlockRequiredError{Backend: secrets.BackendFile}
			}
			return survey.NewSurvey().
				Question(&survey.QuestionOptions{Question: "Enter current secrets passphrase", IsPassword: true})
		},
	}
}

func newProtectionStatusCmd(global *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show file-store protection and availability",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProtectionStatus(cmd, global) },
	}
}

func runProtectionStatus(cmd *cobra.Command, global *flags.GlobalFlags) error {
	manager, err := protectionManager()
	if err != nil {
		return err
	}
	result, err := manager.Status()
	if err != nil {
		return err
	}
	mode, err := output.ResolveMode(global.ResultFormat)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"File protection: %s\nStatus: %s\nRemembered credential: %t\nFile-backed secrets: %d\n",
		result.KeySource,
		result.Availability,
		result.Remembered,
		len(result.FileEntries),
	)
	return err
}

func newProtectionInputCmd(action string) *cobra.Command {
	var stdin bool
	cmd := &cobra.Command{
		Use:   action,
		Short: protectionDescription(action),
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProtectionInput(cmd, action, stdin) },
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "Read the passphrase from standard input")
	return cmd
}

func runProtectionInput(cmd *cobra.Command, action string, stdin bool) error {
	manager, err := protectionManager()
	if err != nil {
		return err
	}
	value, err := protectionInput(cmd, manager, action, stdin)
	if err != nil {
		return err
	}
	if len(value) < 12 {
		_, _ = fmt.Fprintln(
			cmd.ErrOrStderr(),
			"Warning: use a long passphrase (at least 12 characters) to protect the file store.",
		)
	}
	switch action {
	case protectionRemember:
		return manager.Remember(value)
	case protectionSetPassphrase:
		return manager.SetPassphrase(value)
	default:
		return manager.ChangePassphrase(value)
	}
}

func protectionInput(
	cmd *cobra.Command,
	p *secrets.ProtectionManager,
	action string,
	stdin bool,
) (string, error) {
	if action == protectionRemember && !stdin {
		return p.ResolvePassphrase(cmd.Context())
	}
	return readProtectionPassphrase(cmd, stdin)
}

func newProtectionMutationCmd(
	action, description string,
	run func(*secrets.ProtectionManager) error,
) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			p, err := protectionManager()
			if err != nil {
				return err
			}
			return run(p)
		},
	}
}

func newProtectionResetCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset-file-store",
		Short: "Quarantine the entire file store and remove its catalog entries",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runProtectionReset(cmd, yes) },
	}
	cmd.Flags().
		BoolVar(&yes, "yes", false, "Confirm removal of ALL file-backed secrets across every context")
	return cmd
}

func runProtectionReset(cmd *cobra.Command, yes bool) error {
	p, err := protectionManager()
	if err != nil {
		return err
	}
	status, err := p.CatalogStatus()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(
		cmd.ErrOrStderr(),
		"Reset removes every file-backed secret across all contexts:",
	)
	for _, meta := range status.FileEntries {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s/%s\n", meta.Context, meta.Name)
	}
	if err = confirmProtectionReset(yes); err != nil {
		return err
	}
	quarantine, err := p.ResetFileStoreIfUnchanged(status.FileEntries)
	if err != nil {
		return err
	}
	if quarantine != "" {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Encrypted file quarantined at %s\n", quarantine)
	}
	return err
}

func confirmProtectionReset(yes bool) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("reset requires explicit destructive confirmation with --yes")
	}
	answer, err := survey.NewSurvey().
		Question(&survey.QuestionOptions{Question: "Type RESET to quarantine the file store and remove all listed secrets"})
	if err != nil {
		return err
	}
	if answer != "RESET" {
		return errors.New("file-store reset cancelled")
	}
	return nil
}

func protectionDescription(action string) string {
	switch action {
	case protectionRemember:
		return "Verify and remember a passphrase in the OS keychain"
	case protectionSetPassphrase:
		return "Re-encrypt the file store with a new passphrase"
	default:
		return "Change the file-store passphrase"
	}
}

func protectionManager() (*secrets.ProtectionManager, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, err
	}
	return secrets.NewProtectionManager(filepath.Dir(path), protectionResolver()), nil
}

func readProtectionPassphrase(cmd *cobra.Command, stdin bool) (string, error) {
	var value string
	var err error
	if stdin {
		value, err = readTrimmed(cmd.InOrStdin(), "stdin")
	} else {
		value, err = promptNewProtectionPassphrase()
	}
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", errors.New("passphrase must not be empty")
	}
	return value, nil
}

func promptNewProtectionPassphrase() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("new passphrase requires --stdin or an interactive terminal")
	}
	value, err := survey.NewSurvey().
		Question(&survey.QuestionOptions{Question: "Enter new secrets passphrase", IsPassword: true})
	if err != nil {
		return "", err
	}
	confirmation, err := survey.NewSurvey().
		Question(&survey.QuestionOptions{Question: "Confirm new secrets passphrase", IsPassword: true})
	if err != nil {
		return "", err
	}
	if value != confirmation {
		return "", errors.New("passphrases do not match")
	}
	return value, nil
}

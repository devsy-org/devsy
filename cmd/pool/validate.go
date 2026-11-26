package pool

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/spf13/cobra"
)

type ValidateCmd struct {
	*flags.GlobalFlags

	File string
}

type validateResult struct {
	Name          string `json:"name"`
	PolicyVersion string `json:"policyVersion"`
	ConfigVersion string `json:"configVersion"`
}

func NewValidateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ValidateCmd{GlobalFlags: globalFlags}
	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Check a pool file and print its policy and config versions",
		Long: `Check a pool file and print its policy and config versions.

The policy version covers what governs a workspace. The config version covers
the whole file, including capacity and audit settings.

A pool file must hold no secrets. Env values, command arguments and provider
options are kept as written and feed the policy version. Keep keys in a secret
store and reference them from a credential grant as TYPE:SOURCE/NAME, for
example sops:pool/API_KEY.`,
		Args: cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.OutOrStdout())
		},
	}
	validateCmd.Flags().StringVarP(&cmd.File, names.File, "f", "pool.yaml", "Path to the pool file")
	return validateCmd
}

func (cmd *ValidateCmd) Run(w io.Writer) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	p, err := poolconfig.Load(cmd.File)
	if err != nil {
		return err
	}
	policy, err := p.PolicyVersion()
	if err != nil {
		return err
	}
	config, err := p.ConfigVersion()
	if err != nil {
		return err
	}
	result := validateResult{Name: p.Metadata.Name, PolicyVersion: policy, ConfigVersion: config}
	if mode == output.ModeJSON {
		return json.NewEncoder(w).Encode(result)
	}
	_, err = fmt.Fprintf(
		w,
		"pool %s is valid\npolicy version: %s\nconfig version: %s\n",
		result.Name,
		result.PolicyVersion,
		result.ConfigVersion,
	)
	return err
}

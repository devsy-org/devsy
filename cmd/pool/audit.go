package pool

import (
	"errors"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/spf13/cobra"
)

const (
	auditDirFlag     = "dir"
	auditSessionFlag = "session"
	auditSinceFlag   = "since"
)

var errAuditSince = errors.New("--since must be an RFC3339 timestamp or a YYYY-MM-DD UTC date")

type auditCmd struct {
	dir     string
	session string
	since   string
	output  string
}

func NewAuditCmd() *cobra.Command {
	cmd := &auditCmd{}
	command := &cobra.Command{
		Use:          "audit",
		Short:        "Read local pool audit records as newline-delimited JSON",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			if cobraCmd.Flags().Changed(auditSinceFlag) && cmd.since == "" {
				return errAuditSince
			}
			return cmd.Run(cobraCmd.OutOrStdout())
		},
	}
	f := command.Flags()
	f.StringVar(&cmd.dir, auditDirFlag, "", "Path to the local audit directory (required)")
	f.StringVar(&cmd.session, auditSessionFlag, "", "Only include records for this session ID")
	f.StringVar(
		&cmd.since,
		auditSinceFlag,
		"",
		"Include records at or after an RFC3339 time or UTC date",
	)
	f.StringVar(
		&cmd.output,
		names.Output,
		output.ModeJSON,
		"Output format (only json is supported)",
	)
	_ = command.MarkFlagRequired(auditDirFlag)
	return command
}

func (cmd *auditCmd) Run(w io.Writer) error {
	if cmd.dir == "" {
		return errors.New("--dir must not be empty")
	}
	mode, err := output.ResolveMode(cmd.output)
	if err != nil {
		return err
	}
	if mode != output.ModeJSON {
		return errors.New("pool audit only supports --output json")
	}
	since, err := parseAuditSince(cmd.since)
	if err != nil {
		return err
	}
	reader, err := audit.OpenReader(cmd.dir)
	if err != nil {
		return err
	}
	return reader.Scan(
		audit.ReaderFilter{Session: cmd.session, Since: since},
		func(line []byte) error {
			_, err := w.Write(append(line, '\n'))
			return err
		},
	)
}

func parseAuditSince(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.DateOnly, value); err == nil {
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errAuditSince
	}
	utc := t.UTC()
	return &utc, nil
}
